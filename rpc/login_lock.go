package rpc

// A3-phase2: 跨节点登录互斥 & 定向踢 owner。
// F3（v3）: 会话与踢人收尾——
//   - 登录锁 watchdog 续期（审计1：5s TTL 无续期，关键路径最坏可超 TTL）；
//     锁过期 / 释放失败通过 Warn 日志 + tgf_gate_login_lock_lost_total 计数器可感知。
//   - gate owner meta 写入带 TTL，且自然下线时原子比对清理（审计8：网关重启产生永久脏 owner）。
//   - KickRemoteOwner 改为同步带 ack 的 RPC（远端完成全部清理含 OfflineHook 后才返回），
//     移除原先 Oneshot + 固定 sleep(remoteKickWait=200ms) 的时序赌博。
//   - 进程内登录协调器 inProcLoginCoordinator（D 遗留）：单进程 / 无 Redis 部署下
//     登录互斥与 owner 语义降级为进程内实现，使纯单进程可真实登录。
//
// 本文件提供 loginCoordinator 接口封装 Login 流程依赖的几个副作用：
//   - 登录锁的获取 / 释放（Redis 分布式锁或进程内锁）
//   - 读 / 写 / 比对清理 user:node:meta 里的 gate owner 字段
//   - 向远端 gate 节点同步发 Offline RPC（带 ack）
//
// gate.Login 只依赖这个接口，默认走 defaultLoginCoordinator（生产路径），
// 测试通过 package-level var 的替换注入 fakeLoginCoordinator。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
	"github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/metrics"
	"github.com/thkhxm/tgf/rpc/internal"
	"github.com/thkhxm/tgf/util"
)

// loginLockHandle 包装登录锁的释放句柄。生产实现是 *redislockHandle（Redis）
// 或 *inProcLockHandle（进程内），测试 mock 用任意占位 struct。
type loginLockHandle interface {
	isLoginLock()
}

// loginCoordinator 是 Login 流程的副作用接口，独立于 gate.go 方便 mock。
type loginCoordinator interface {
	// AcquireLoginLock 获取 userId 的登录锁；带内部重试和短退避。
	// 失败返回 error；成功返回的 handle 必须被 ReleaseLoginLock 释放。
	// F3：Redis 路径的 handle 自带 watchdog 续期 goroutine，持锁期间每
	// loginLockRefreshInterval 刷新一次 TTL，保证关键路径（踢人 + Hook）超过
	// 初始 TTL 时锁不静默过期。
	AcquireLoginLock(userId string) (loginLockHandle, error)

	// ReleaseLoginLock 释放锁。即使锁已过期或释放过，调用也应是幂等的。
	// F3：锁释放时发现已不持有（TTL 过期被抢占）会记 Warn + metrics，可感知。
	ReleaseLoginLock(h loginLockHandle)

	// GetGateOwner 读取 user:node:meta hash 里的 gate owner（rpcx service address）。
	// 没有 owner 返回空串。
	GetGateOwner(userId string) string

	// SetGateOwner 把本节点写成 gate owner。F3：写入带 TTL（与业务路由 meta 同
	// 7 天），兜底防止 owner 字段永久残留。
	SetGateOwner(userId, address string)

	// ClearGateOwner 原子比对清理 owner：仅当当前 owner == expectAddress 时删除。
	// F3 新增（审计8）：自然下线（非替换踢人）时由网关调用，清掉指向本节点的
	// owner，避免网关重启 / 换地址后每次登录都对死地址发踢人 RPC。
	// 比对删除保证不误删并发新登录刚写入的新 owner。
	ClearGateOwner(userId, expectAddress string)

	// KickRemoteOwner 对 owner 节点定向发 Offline RPC。
	// F3：同步带 ack——远端 gate.Offline 完成全部清理（含旧会话 OfflineHook 广播）
	// 后才返回，调用方无需再 sleep 等待远端清理窗口。
	// 注意：这里依赖 rpcx 的 "service@address" 路由能力，不走 BorderRPCMessage 广播。
	KickRemoteOwner(address, userId string) error
}

// loginCoord 是 loginCoordinator 的 package 单例。测试里可以临时替换它。
var loginCoord loginCoordinator = &defaultLoginCoordinator{}

// defaultLoginCoordinator 是生产实现。
// F3：Redis 未配置（单进程 / CacheModuleClose / db 未启动）时按方法委托给
// inProcLoginCoord——判定依据是配置形态（GetRedisClient 是否为 nil），不是
// 运行时健康探测，因此不会在 Redis 抖动时悄悄降级跨节点互斥强度。
type defaultLoginCoordinator struct{}

const (
	// loginLockRetries 是 AcquireLoginLock 的最大尝试次数。
	// db.NewLock 内部 TTL 是 loginLockTTL，本方允许的总等待时间 ≈ retries*backoff。
	loginLockRetries = 5
	loginLockBackoff = 100 * time.Millisecond

	// loginLockTTL 必须与 db 层 redisService.TryLock 的 Obtain TTL（5 秒）一致——
	// watchdog 每次 Refresh 都把剩余 TTL 重置回这个值。
	loginLockTTL = 5 * time.Second

	// loginLockOpTimeout 是 watchdog Refresh / Release 单次 Redis 操作的超时上限，
	// 避免 Redis 慢时 watchdog / 释放路径无限阻塞。
	loginLockOpTimeout = time.Second
)

// loginLockRefreshInterval 是 watchdog 的续期周期（TTL 的一半）。
// 做成 var 便于单测缩短到毫秒级验证续期行为。
var loginLockRefreshInterval = loginLockTTL / 2

// ---- F3 锁可观测性：锁丢失计数器 ----

var (
	loginLockMetricsOnce sync.Once
	loginLockLostTotal   metrics.Counter
)

// incLoginLockLost 在"持锁期间锁过期 / 被抢占"被探测到时（watchdog 续期失败、
// 释放时 ErrLockNotHeld）累加。延迟初始化原因同 tcp.go 的 gateConnGauge。
func incLoginLockLost() {
	loginLockMetricsOnce.Do(func() {
		loginLockLostTotal = metrics.NewCounter(
			"tgf_gate_login_lock_lost_total",
			"登录锁在持有期间过期/被抢占的次数(watchdog 续期失败 + 释放时未持有)",
		)
	})
	loginLockLostTotal.Inc()
}

// resetLoginLockMetricsOnceForTest 只用于单测，允许切换 Provider 后重新绑定计数器。
func resetLoginLockMetricsOnceForTest() {
	loginLockMetricsOnce = sync.Once{}
	loginLockLostTotal = nil
}

// ---- Redis 登录锁 handle（带 watchdog 续期） ----

// redislockHandle 是 loginLockHandle 对 *redislock.Lock 的包装。
// refreshFn / releaseFn 默认绑定真实 lock 的 Refresh / Release，
// 单测注入 fake 以便在无 Redis 环境下验证 watchdog 行为。
type redislockHandle struct {
	key       string
	lock      *redislock.Lock
	refreshFn func(ctx context.Context, ttl time.Duration) error
	releaseFn func(ctx context.Context) error
	stop      chan struct{} // close 后 watchdog 退出
	done      chan struct{} // watchdog 退出后 close
	released  atomic.Bool   // 保证 release 幂等
}

func (*redislockHandle) isLoginLock() {}

func newRedislockHandle(key string, lock *redislock.Lock) *redislockHandle {
	return &redislockHandle{
		key:  key,
		lock: lock,
		refreshFn: func(ctx context.Context, ttl time.Duration) error {
			return lock.Refresh(ctx, ttl, nil)
		},
		releaseFn: func(ctx context.Context) error {
			return lock.Release(ctx)
		},
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// watchdog 持锁期间周期性续期。续期失败说明锁已过期 / key 被抢占——记 Warn +
// 计数器后退出（不再无谓续期）。审计1 的"锁静默过期"由此变为可感知事件。
func (h *redislockHandle) watchdog() {
	defer close(h.done)
	ticker := time.NewTicker(loginLockRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), loginLockOpTimeout)
			err := h.refreshFn(ctx, loginLockTTL)
			cancel()
			if err != nil {
				incLoginLockLost()
				log.WarnTag("gate", "登录锁续期失败,锁可能已过期被其他节点抢占 key=%v err=%v", h.key, err)
				return
			}
		}
	}
}

// release 幂等释放：先停 watchdog（等它真正退出，避免释放后还在续期别人的锁），
// 再释放 Redis 锁。ErrLockNotHeld 意味着锁在持有期间已过期——这正是审计1 要求
// 可感知的"关键路径超 TTL"信号。
func (h *redislockHandle) release() {
	if !h.released.CompareAndSwap(false, true) {
		return
	}
	close(h.stop)
	<-h.done
	ctx, cancel := context.WithTimeout(context.Background(), loginLockOpTimeout)
	defer cancel()
	if err := h.releaseFn(ctx); err != nil {
		if errors.Is(err, redislock.ErrLockNotHeld) {
			incLoginLockLost()
			log.WarnTag("gate", "释放登录锁时锁已不被持有(TTL 过期或被抢占,锁内关键路径可能超时) key=%v", h.key)
		} else {
			log.WarnTag("gate", "释放登录锁失败 key=%v err=%v", h.key, err)
		}
	}
}

// ---- defaultLoginCoordinator：Redis 生产路径 + 无 Redis 时按方法委托进程内实现 ----

// loginRedisAvailable 判定 Redis 登录后端是否可用。这是配置态判定（db 层是否
// 以 Redis 模式启动），不是健康探测——Redis 配了但临时不可达时仍走 Redis 路径
// 并如实报错，绝不悄悄降级跨节点互斥。做成 var 便于单测强制走进程内路径。
var loginRedisAvailable = func() bool { return db.GetRedisClient() != nil }

var inProcFallbackWarnOnce sync.Once

func warnInProcLoginFallback() {
	inProcFallbackWarnOnce.Do(func() {
		log.WarnTag("gate", "Redis 未配置(单进程/CacheModuleClose),登录互斥降级为进程内锁;"+
			"多节点部署必须配置 Redis,否则跨节点无双在线保证")
	})
}

func (d *defaultLoginCoordinator) AcquireLoginLock(userId string) (loginLockHandle, error) {
	if !loginRedisAvailable() {
		warnInProcLoginFallback()
		return inProcLoginCoord.AcquireLoginLock(userId)
	}
	key := loginLockKey(userId)
	var lastErr error
	for i := 0; i < loginLockRetries; i++ {
		lock, err := db.NewLock(key)
		if err == nil {
			h := newRedislockHandle(key, lock)
			// F3：启动 watchdog 续期——锁内关键路径（同步踢人 ack + LoginHook）
			// 超过初始 TTL 时不再静默失锁。
			util.Go(h.watchdog)
			return h, nil
		}
		lastErr = err
		time.Sleep(loginLockBackoff)
	}
	return nil, fmt.Errorf("acquire login lock key=%s failed after %d retries: %w",
		key, loginLockRetries, lastErr)
}

func (d *defaultLoginCoordinator) ReleaseLoginLock(h loginLockHandle) {
	// 按 handle 类型分发：Acquire 可能委托给了进程内实现，释放必须跟随句柄本身。
	switch rh := h.(type) {
	case *redislockHandle:
		if rh != nil {
			rh.release()
		}
	case *inProcLockHandle:
		if rh != nil {
			rh.release()
		}
	}
}

func (d *defaultLoginCoordinator) GetGateOwner(userId string) string {
	if !loginRedisAvailable() {
		return inProcLoginCoord.GetGateOwner(userId)
	}
	meta, ok := db.GetMap[string, string](fmt.Sprintf(tgf.RedisKeyUserNodeMeta, userId))
	if !ok {
		return ""
	}
	return meta[tgf.GatewayServiceModuleName]
}

func (d *defaultLoginCoordinator) SetGateOwner(userId, address string) {
	if !loginRedisAvailable() {
		inProcLoginCoord.SetGateOwner(userId, address)
		return
	}
	// F3（审计8）：写入带 TTL。user:node:meta 与业务路由（plugins.go processNode）
	// 共用一个 hash key，这里沿用同一份 reqMetaDataTimeout（7 天），每次登录刷新——
	// 网关全体下线后 owner 不再永久存活。主动清理在 Offline 链路（ClearGateOwner）。
	db.PutMap(fmt.Sprintf(tgf.RedisKeyUserNodeMeta, userId),
		tgf.GatewayServiceModuleName, address, reqMetaDataTimeout)
}

// clearGateOwnerScript 原子比对删除 owner 字段：HGET == expect 才 HDEL。
// 用 Lua 保证"读-比-删"原子性，避免与并发登录的 SetGateOwner 交错时误删新 owner。
const clearGateOwnerScript = `if redis.call('HGET', KEYS[1], ARGV[1]) == ARGV[2] then return redis.call('HDEL', KEYS[1], ARGV[1]) else return 0 end`

func (d *defaultLoginCoordinator) ClearGateOwner(userId, expectAddress string) {
	if !loginRedisAvailable() {
		inProcLoginCoord.ClearGateOwner(userId, expectAddress)
		return
	}
	rc := db.GetRedisClient()
	key := fmt.Sprintf(tgf.RedisKeyUserNodeMeta, userId)
	err := rc.Eval(context.Background(), clearGateOwnerScript,
		[]string{key}, tgf.GatewayServiceModuleName, expectAddress).Err()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.WarnTag("gate", "清理 gate owner 失败 uid=%v expect=%v err=%v", userId, expectAddress, err)
	}
}

func (d *defaultLoginCoordinator) KickRemoteOwner(address, userId string) error {
	return kickOwnerSyncByAddress(address, userId)
}

// kickOwnerSyncByAddress 对指定 gate 节点同步发 Offline（replace=true）并等待返回。
// F3（审计：踢人时序错配）：原实现 Oneshot 不等回执 + 本端 sleep 200ms，而远端
// 清理路径含 1s 强制 sleep——旧会话 OfflineHook 必然晚于新会话上线。现在远端
// gate.Offline 同步完成全部清理（含 OfflineHook）后才返回，本端拿到 ack 再继续
// DoLogin，时序由 RPC 语义保证而非 sleep 赌博。
// 进程内（单进程模式）命中 local dispatcher 时同样是同步调用，ack 语义一致。
func kickOwnerSyncByAddress(address, userId string) (err error) {
	release, perr := applyMethodPolicy(tgf.GatewayServiceModuleName, "Offline")
	if perr != nil {
		return perr
	}
	defer func() {
		release(err)
	}()
	args := &OfflineReq{UserId: userId, Replace: true}
	reply := &OfflineRes{}

	// 单进程 fast path：本地反射同步调用，天然带 ack。
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(tgf.GatewayServiceModuleName); ok {
			err = localDispatcher.Call(context.Background(),
				tgf.GatewayServiceModuleName, "Offline", args, reply)
			return err
		}
	}

	rc := getRPCClient()
	xclient := rc.getClient(tgf.GatewayServiceModuleName)
	if xclient == nil {
		err = tgf.ServiceNotFound
		return err
	}
	// 与 SendRPCMessageByStr 相同的 Go + select 超时模式（ctx 不能用
	// context.WithTimeout 包装，会破坏 rpcx 对 *share.Context 的类型断言）。
	done := make(chan *client.Call, 1)
	call, gerr := xclient.Go(newRPCNodeContext(tgf.GatewayServiceModuleName, address),
		"Offline", args, reply, done)
	if gerr != nil {
		err = fmt.Errorf("定向踢 owner 请求异常 address=%v uid=%v error=%w", address, userId, gerr)
		return err
	}
	if call == nil {
		err = fmt.Errorf("定向踢 owner 无可用服务节点 address=%v uid=%v", address, userId)
		return err
	}
	select {
	case <-time.After(resolveRPCTimeout(tgf.GatewayServiceModuleName, "Offline")):
		err = tgf.ErrorRPCTimeOut
	case <-call.Done:
		err = call.Error
	}
	return err
}

// ---- F3：进程内登录协调器（单进程 / 无 Redis 部署，D 遗留收尾） ----

// inProcLockHandle 是进程内锁的释放句柄。
type inProcLockHandle struct {
	sem      chan struct{}
	released atomic.Bool
}

func (*inProcLockHandle) isLoginLock() {}

func (h *inProcLockHandle) release() {
	if !h.released.CompareAndSwap(false, true) {
		return
	}
	<-h.sem
}

// inProcLoginLockWait 是进程内锁的最大等待时间，与 Redis 路径
// retries*backoff 的总等待（≈500ms）对齐。var 便于单测缩短。
var inProcLoginLockWait = loginLockRetries * loginLockBackoff

// inProcLoginCoordinator 在单进程 / 无 Redis 部署下提供与 Redis 路径同语义的
// 登录互斥 + owner 元数据：
//   - 每个 userId 一个容量 1 的信号量（带超时获取），等价于登录锁；
//   - owners 内存表承载 GetGateOwner / SetGateOwner / ClearGateOwner；
//   - KickRemoteOwner 复用同一条同步踢人路径（单进程下 owner 恒为本进程地址，
//     正常不会触发远端分支）。
//
// 进程内锁没有 TTL——进程崩溃即全部状态消失，不存在残留锁；因此无需 watchdog。
type inProcLoginCoordinator struct {
	mu     sync.Mutex
	sems   map[string]chan struct{}
	owners map[string]string
}

// inProcLoginCoord 是进程级单例，defaultLoginCoordinator 在无 Redis 时按方法委托到它。
var inProcLoginCoord = &inProcLoginCoordinator{
	sems:   make(map[string]chan struct{}),
	owners: make(map[string]string),
}

func (c *inProcLoginCoordinator) semFor(userId string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	sem, ok := c.sems[userId]
	if !ok {
		sem = make(chan struct{}, 1)
		c.sems[userId] = sem
	}
	return sem
}

func (c *inProcLoginCoordinator) AcquireLoginLock(userId string) (loginLockHandle, error) {
	sem := c.semFor(userId)
	select {
	case sem <- struct{}{}:
		return &inProcLockHandle{sem: sem}, nil
	case <-time.After(inProcLoginLockWait):
		return nil, fmt.Errorf("acquire in-proc login lock key=%s timeout after %v",
			loginLockKey(userId), inProcLoginLockWait)
	}
}

func (c *inProcLoginCoordinator) ReleaseLoginLock(h loginLockHandle) {
	if rh, ok := h.(*inProcLockHandle); ok && rh != nil {
		rh.release()
	}
}

func (c *inProcLoginCoordinator) GetGateOwner(userId string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owners[userId]
}

func (c *inProcLoginCoordinator) SetGateOwner(userId, address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.owners[userId] = address
}

func (c *inProcLoginCoordinator) ClearGateOwner(userId, expectAddress string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owners[userId] == expectAddress {
		delete(c.owners, userId)
	}
}

func (c *inProcLoginCoordinator) KickRemoteOwner(address, userId string) error {
	return kickOwnerSyncByAddress(address, userId)
}

// ---- 公共小工具 ----

// loginLockKey 返回 Redis 登录锁的 key。独立出来方便 phase2 测试断言 key 格式，
// 也方便未来按 key 前缀聚合埋点。
func loginLockKey(userId string) string {
	return "tgf:gate:login:lock:" + userId
}

// localGateAddress 返回本节点 gate 的 rpcx service address。
// 独立成函数方便测试里 mock（通过替换 globalLocalAddressFn）。
// F3：单进程 / 未注册 rpcx 服务时 internal.LocalServerAddress 为空串——返回
// 进程内哨兵地址，保证 owner 写入与比对在同进程内自洽（重复登录能命中
// "本地 owner → 本地踢人"分支，而不是恒为"无 owner"导致进程内双在线）。
func localGateAddress() string {
	if globalLocalAddressFn != nil {
		return globalLocalAddressFn()
	}
	if internal.LocalServerAddress != "" {
		return internal.LocalServerAddress
	}
	return "local@in-proc"
}

// globalLocalAddressFn 是 localGateAddress 的测试钩子；非 nil 时覆盖默认实现。
var globalLocalAddressFn func() string
