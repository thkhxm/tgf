package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description E4 · 补偿队列"默认启用 + 启动 replay"真实接线
//
// C4 落地的 FailureQueue 是半成品（V3 审计 P2）：
//   1. 默认 Noop——开箱状态下补偿队列不存在，"跨重启恢复"是空头支票；
//   2. ReplayFailureQueue 框架内零调用，且真正能执行 upsert 的 flushBatch
//      全部未导出，业务拿到 payload 后只能自己手写整条 INSERT ... ON DUPLICATE。
//
// 本文件把链路闭环：
//   - 默认队列升级为进程级 FileFailureQueue（懒加载；路径可经
//     SetDefaultFailureQueuePath 在首次使用前定制）；
//   - 每个 longevity 管理器在 InitStruct 时按表名注册 replay flusher
//     （见 manager.go wireFailureReplay），并立刻对队列做一轮启动重放；
//   - ReplayPayload 导出给业务侧手动重放单条 payload（内部同样走表名注册表
//     分发到真实 flushBatch）。
//
//2026/6/10
//***************************************************

import (
	"errors"
	"fmt"
	"sync"

	"github.com/thkhxm/tgf/log"
)

// ---- 表名 → flusher 注册表 ----

// payloadFlusher 把一条已解码的 payload（values + count）真实落库的函数。
// 由各 longevity 管理器在 InitStruct 时注册，指向自己的 flushBatch。
type payloadFlusher func(values []any, count int) error

var (
	replayRegistryMu sync.RWMutex
	replayRegistry   = map[string]payloadFlusher{}
)

// errNoReplayFlusher 表示 payload 所属表还没有注册 replay flusher
// （对应的 longevity 管理器尚未创建）。startup replay 对该错误的处理是
// "重新入队、不算失败"——等管理器创建时再重放。
var errNoReplayFlusher = errors.New("db/failure: no replay flusher registered")

func registerReplayFlusher(table string, fn payloadFlusher) {
	if table == "" || fn == nil {
		return
	}
	replayRegistryMu.Lock()
	replayRegistry[table] = fn
	replayRegistryMu.Unlock()
}

func lookupReplayFlusher(table string) payloadFlusher {
	replayRegistryMu.RLock()
	defer replayRegistryMu.RUnlock()
	return replayRegistry[table]
}

// ReplayPayload 把一条补偿队列 payload 重新落库。
// 内部按 payload 的表名找到对应 longevity 管理器注册的 flushBatch 真实执行
// upsert（与周期落库完全同一条 SQL 路径，幂等）。
// 表未注册（对应管理器尚未创建）时返回 errNoReplayFlusher 包装错误。
func ReplayPayload(p FailurePayload) error {
	doc, err := DecodeFailurePayload(p)
	if err != nil {
		return err
	}
	fn := lookupReplayFlusher(doc.Table)
	if fn == nil {
		return fmt.Errorf("%w: table=%s", errNoReplayFlusher, doc.Table)
	}
	if ferr := fn(doc.Values, doc.Count); ferr != nil {
		return fmt.Errorf("db/failure: replay 落库失败 table=%s count=%d: %w", doc.Table, doc.Count, ferr)
	}
	return nil
}

// replayQueueForKnownTables Drain 队列并重放所有"已注册表"的 payload：
//   - 重放成功 → 从队列移除（Drain 已取出，不再回写）；
//   - 表未注册 → 重新入队（不计入错误，等对应管理器创建时再重放）；
//   - 解码失败 / 落库失败 → 重新入队保底不丢，并聚合进返回的 err。
//
// 由 wireFailureReplay（每个 longevity 管理器 InitStruct 时）调用，
// 业务也可在自定义恢复流程里复用。
func replayQueueForKnownTables(q FailureQueue) (replayed, requeued int, err error) {
	if q == nil {
		return 0, 0, nil
	}
	payloads, derr := q.Drain()
	if derr != nil {
		return 0, 0, fmt.Errorf("db/failure: drain 失败: %w", derr)
	}
	var errs []error
	for _, p := range payloads {
		rerr := ReplayPayload(p)
		if rerr == nil {
			replayed++
			mcFailureReplayTotal().Inc()
			continue
		}
		// 失败/未注册：重新入队，保证数据不丢。
		requeued++
		if errors.Is(rerr, errNoReplayFlusher) {
			log.DebugTag("orm", "补偿队列 payload 所属表尚未注册, 重新入队等待 err=%v", rerr)
		} else {
			errs = append(errs, rerr)
			mcFailureReplayFailTotal().Inc()
		}
		if eerr := q.Enqueue(p); eerr != nil {
			log.WarnTag("orm", "补偿队列重放失败且重新入队失败(payload 可能丢失) err=%v enqueueErr=%v", rerr, eerr)
			errs = append(errs, fmt.Errorf("db/failure: 重新入队失败: %w", eerr))
		}
	}
	mgFailureQueueDepth().Set(float64(q.Len()))
	return replayed, requeued, errors.Join(errs...)
}

// ---- 进程级默认补偿队列（E4：默认实现从 Noop 升级为 File）----

var (
	defaultFailureQueueMu   sync.Mutex
	defaultFailureQueueInst FailureQueue
	defaultFailureQueuePath = "./longevity_failures.log"
)

// SetDefaultFailureQueuePath 设置进程级默认补偿队列的落盘路径。
// 必须在默认队列首次被使用（首次落库失败 / 首个 longevity 管理器创建触发
// 启动 replay）之前调用；默认队列已初始化后再调用返回 error。
// 零配置时使用工作目录下 ./longevity_failures.log（与 doc/example 一致）。
func SetDefaultFailureQueuePath(path string) error {
	if path == "" {
		return errors.New("db/failure: path 不能为空")
	}
	defaultFailureQueueMu.Lock()
	defer defaultFailureQueueMu.Unlock()
	if defaultFailureQueueInst != nil {
		return errors.New("db/failure: 默认补偿队列已初始化, 修改路径无效(请在首个 longevity 管理器创建前调用)")
	}
	defaultFailureQueuePath = path
	return nil
}

// defaultFailureQueue 懒加载进程级默认 FileFailureQueue。
// 文件打不开（权限 / 路径问题）时降级为 Noop 并打 ERROR——补偿队列是兜底通道，
// 不能因它把主流程打挂；降级事实通过日志与队列深度指标可观测。
func defaultFailureQueue() FailureQueue {
	defaultFailureQueueMu.Lock()
	defer defaultFailureQueueMu.Unlock()
	if defaultFailureQueueInst == nil {
		q, err := NewFileFailureQueue(defaultFailureQueuePath)
		if err != nil {
			log.ErrorTag("orm", "默认补偿队列(File)初始化失败,降级为 Noop path=%s err=%v", defaultFailureQueuePath, err)
			defaultFailureQueueInst = NoopFailureQueue{}
		} else {
			log.InfoTag("orm", "默认补偿队列(File)已启用 path=%s backlog=%d", defaultFailureQueuePath, q.Len())
			defaultFailureQueueInst = q
		}
	}
	return defaultFailureQueueInst
}
