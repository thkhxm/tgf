package db

// C4 / A1b · 落库失败补偿队列
//
// 背景：
//  A1 阶段重写了 autoCacheManager.toLongevity 的可靠性路径——flushBatch 内部做
//  指数退避的 3 次重试，耗尽后保留 data_update 标志，交给下一轮 timer 补偿。
//  这个机制处理"瞬时故障"（DB 抖动、网络闪断）够用，但对于"持续故障"有三个盲区：
//
//    1. 进程被 kill 时脏数据丢失——脏标志只在内存里
//    2. 数据库长时间不可用，脏数据无上限堆积，OOM 风险
//    3. 跨重启无法恢复——重启后 cache 重建，脏数据彻底消失
//
// A1 当时把这块标为 A1b，挪到 C 档配合分库分表抽象一起做。C4 落地了
// **可插拔 FailureQueue 接口**；E4 把默认实现从 NoOp 升级为进程级
// FileFailureQueue（failure_replay.go），并内置启动 replay——开箱即得
// "落库失败跨重启可恢复"。业务仍可接入 Redis list、RocksDB、Kafka 等
// 持久化实现，按自己的可靠性 SLA 选实现。
//
// 接入点：
//   - 默认零配置：失败批次自动进 ./longevity_failures.log（路径可经
//     SetDefaultFailureQueuePath 定制），longevity 管理器创建时自动重放
//   - 构造 AutoCacheBuilder 时调 WithLongevityFailureQueue(q) 注入自定义实现，
//     显式传 NoopFailureQueue{} 关闭补偿队列
//   - toLongevity 里 flushBatch 失败后，把 batch 序列化进 queue
//   - 业务也可手动调 db.ReplayFailureQueue(queue, flushFn) /
//     db.ReplayPayload(p) 重放
//
// 失败语义：
//   - q.Enqueue 本身出错 → 日志 WARN 不抛——主流程不能被降级通道打挂
//   - q.Drain 失败 → 日志 WARN，整个 Replay 中止，business 需要人工介入

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/thkhxm/tgf/log"
)

// FailurePayload 是 FailureQueue 的最小单元：一次落库批次的原始字节。
// 字节格式由调用方决定——序列化/反序列化对 FailureQueue 透明。
type FailurePayload []byte

// FailureQueue 是补偿队列接口。实现方负责把 FailurePayload 持久化到某种
// 存储（文件 / Redis / Kafka / ...），并在 Drain 时按入队顺序返回。
//
// 线程安全：FailureQueue 的所有方法必须支持多 goroutine 并发调用。
// toLongevity 是串行执行的（longevityLock），但测试和业务自定义路径
// 可能并发触发 Enqueue。
type FailureQueue interface {
	// Enqueue 入队一个 payload。返回 error 表示队列自身故障（磁盘满、文件被删、
	// 权限问题等），调用方应当记录日志但不应中断主流程。
	Enqueue(p FailurePayload) error
	// Drain 返回队列里所有累积的 payload，按入队顺序排序，同时清空队列。
	// 启动阶段调用一次，结果交给 replayFn 重放。
	Drain() ([]FailurePayload, error)
	// Len 返回当前队列内 payload 数量，0 表示空。仅供观察/日志使用，
	// 不保证和并发 Enqueue 严格一致（非原子观察）。
	Len() int
	// Close 释放资源（关闭文件句柄 / flush buffer 等）。幂等。
	Close() error
}

// ---- NoopFailureQueue ----

// NoopFailureQueue 是默认 FailureQueue：什么都不做，行为和 A1 之前一致。
// 业务不注入时 AutoCacheBuilder 内部用这个作为零值，保证接口调用不会 nil pointer。
type NoopFailureQueue struct{}

func (NoopFailureQueue) Enqueue(_ FailurePayload) error { return nil }
func (NoopFailureQueue) Drain() ([]FailurePayload, error) {
	return nil, nil
}
func (NoopFailureQueue) Len() int     { return 0 }
func (NoopFailureQueue) Close() error { return nil }

// ---- MemoryFailureQueue ----

// MemoryFailureQueue 是一个内存实现，仅供单测使用。生产环境应当用 file / redis。
type MemoryFailureQueue struct {
	mu    sync.Mutex
	items []FailurePayload
}

// NewMemoryFailureQueue 返回一个空的内存 FailureQueue。
func NewMemoryFailureQueue() *MemoryFailureQueue {
	return &MemoryFailureQueue{}
}

func (m *MemoryFailureQueue) Enqueue(p FailurePayload) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 拷贝一份避免调用方复用 slice 被后续改动污染
	cp := make(FailurePayload, len(p))
	copy(cp, p)
	m.items = append(m.items, cp)
	return nil
}

func (m *MemoryFailureQueue) Drain() ([]FailurePayload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.items
	m.items = nil
	return out, nil
}

func (m *MemoryFailureQueue) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

func (m *MemoryFailureQueue) Close() error { return nil }

// ---- FileFailureQueue ----

// FileFailureQueue 是一个基于 append-only 文件的简单实现。
//
// 文件格式：每行一个 payload，base64(标准编码) 编码。
// 选择 base64 而不是裸二进制是为了：
//   - 行分隔简单，Drain 时按行扫描
//   - 避免二进制 payload 里的换行符污染分隔
//   - 文本文件便于运维用 cat/grep 肉眼观察
//
// 不足：
//   - Drain 通过"读取整个文件 → 截断文件"两步实现，非原子——如果 Drain 过程中
//     进程崩溃可能丢数据。适合"启动时一次性 replay + 运行中只写不读"的场景。
//   - 没有 segment / rotation，文件会一直涨。业务应当在 Replay 成功后主动调 Close
//     并检查文件大小，必要时重命名归档。
//
// 生产严谨场景建议改用 Kafka / Redis Streams / RocksDB。C4 首版只提供这个最小实现。
type FileFailureQueue struct {
	path string

	mu    sync.Mutex
	file  *os.File
	bufW  *bufio.Writer
	count int
}

// NewFileFailureQueue 打开/创建一个简单的补偿队列文件。
// 文件存在时读取现有行数作为初始 Len()，便于启动期观察。
//
// 注意：不使用 O_APPEND——Windows 上 O_APPEND 和 Truncate 的交互有问题
// （Drain 里 Truncate(0) 会报 Access is denied）。改为手动 Seek 到 EOF 写入。
func NewFileFailureQueue(path string) (*FileFailureQueue, error) {
	// 先统计现有行数
	var count int
	if existing, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(existing)
		scanner.Buffer(make([]byte, 1024*64), 1024*1024*8) // 最大 8MB 一行
		for scanner.Scan() {
			if len(scanner.Bytes()) > 0 {
				count++
			}
		}
		_ = existing.Close()
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("db/FailureQueue: 统计已有行数失败: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("db/FailureQueue: 打开文件失败: %w", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("db/FailureQueue: 创建文件失败: %w", err)
	}
	// 定位到文件末尾，后续 Enqueue 都从这里 append
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("db/FailureQueue: seek end 失败: %w", err)
	}
	return &FileFailureQueue{
		path:  path,
		file:  f,
		bufW:  bufio.NewWriter(f),
		count: count,
	}, nil
}

func (f *FileFailureQueue) Enqueue(p FailurePayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return fmt.Errorf("db/FailureQueue: queue closed")
	}
	// 确保从 EOF 开始写（Drain 之后 bufW 已被重置到 offset=0，需要先 Seek 回 EOF）
	if _, err := f.file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(p)
	if _, err := f.bufW.WriteString(encoded); err != nil {
		return err
	}
	if err := f.bufW.WriteByte('\n'); err != nil {
		return err
	}
	if err := f.bufW.Flush(); err != nil {
		return err
	}
	// 显式 Sync 保证 enqueue 在磁盘上可见——补偿队列的价值就在于崩溃前的写入
	// 能被下次启动看到，fsync 是必要的。代价是性能，业务量大时可以考虑批量 sync。
	if err := f.file.Sync(); err != nil {
		return err
	}
	f.count++
	return nil
}

func (f *FileFailureQueue) Drain() ([]FailurePayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil, fmt.Errorf("db/FailureQueue: queue closed")
	}
	// 刷新 buffer 保证读到最新
	if err := f.bufW.Flush(); err != nil {
		return nil, err
	}
	if _, err := f.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(f.file)
	scanner.Buffer(make([]byte, 1024*64), 1024*1024*8)
	var out []FailurePayload
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(string(line))
		if err != nil {
			return nil, fmt.Errorf("db/FailureQueue: 解码失败: %w", err)
		}
		out = append(out, decoded)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// 截断文件——O_RDWR 无 O_APPEND 时 Truncate 在 Windows 上正常工作
	if err := f.file.Truncate(0); err != nil {
		return nil, err
	}
	if _, err := f.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	f.bufW.Reset(f.file)
	f.count = 0
	return out, nil
}

func (f *FileFailureQueue) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

func (f *FileFailureQueue) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil
	}
	_ = f.bufW.Flush()
	err := f.file.Close()
	f.file = nil
	f.bufW = nil
	return err
}

// ---- Replay helper ----

// ReplayFailureQueue 从 queue 里 Drain 所有 payload，逐个调 flushFn 重放。
// 任何一个 flushFn 返回 error，剩下的未重放的 payload 会重新 Enqueue 回队列，
// 避免"部分 replay 成功后剩余丢失"。
//
// 典型用法：
//
//	q, _ := db.NewFileFailureQueue("./longevity_failures.log")
//	defer q.Close()
//	// Replay 之前必须先让业务配置好 autoCacheManager（好让 flushFn 有效）
//	if err := db.ReplayFailureQueue(q, myFlushFn); err != nil {
//	    log.Error("replay failed: %v", err)
//	}
func ReplayFailureQueue(q FailureQueue, flushFn func(FailurePayload) error) error {
	if q == nil || flushFn == nil {
		return nil
	}
	payloads, err := q.Drain()
	if err != nil {
		return fmt.Errorf("db/FailureQueue: drain 失败: %w", err)
	}
	for i, p := range payloads {
		if err := flushFn(p); err != nil {
			// 把剩下的（包括失败的当前条）重新入队，保证不丢。
			// E4：重新入队失败不再静默吞掉——与文件头宣称的失败语义对齐，
			// 队列自身故障时至少留下可观测的告警。
			for _, remaining := range payloads[i:] {
				if eerr := q.Enqueue(remaining); eerr != nil {
					log.WarnTag("orm", "ReplayFailureQueue 重新入队失败(payload 可能丢失) err=%v", eerr)
				}
			}
			return fmt.Errorf("db/FailureQueue: flushFn 失败 idx=%d: %w", i, err)
		}
	}
	return nil
}
