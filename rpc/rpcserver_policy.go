package rpc

// C6 · RPC 策略化完整版
//
// 基于 A7 的 resolveRPCTimeout 骨架，扩展出一套"按方法名配置策略"的框架：
//
//	type MethodPolicy struct {
//	    Timeout        time.Duration
//	    MaxConcurrency int
//	    RateLimit      float64           // QPS
//	    CircuitBreaker CircuitBreakerConfig
//	}
//
//	rpc.SetMethodPolicy("gate.Login", rpc.MethodPolicy{
//	    Timeout:        3 * time.Second,
//	    MaxConcurrency: 200,
//	    RateLimit:      500,
//	    CircuitBreaker: rpc.CircuitBreakerConfig{
//	        FailureThreshold: 5,
//	        RecoveryInterval: 30 * time.Second,
//	    },
//	})
//
// 执行流程（SendRPCMessage 内部）：
//
//	policy := resolveMethodPolicy(module, method)
//	release, err := policy.BeforeCall()
//	if err != nil {                    // 快速失败：断路器打开 / 限流 / 超并发
//	    return err
//	}
//	defer func() {
//	    release(callErr)                // release 带 err，用于断路器计数
//	}()
//
// 设计要点：
//  1. policy 查找是 fast path：所有 lookup 走 sync.Map + atomic；没配置任何策略
//     时退化成"只查 timeout"，和 A7 行为完全一致
//  2. 各子策略独立：限流 / 并发 / 熔断器可以单独启用或组合使用
//  3. 持久化和热更由 C3 的 config / OnReload 负责——本包只暴露 SetMethodPolicy
//     运行时 API
//  4. metrics：每个策略拒绝路径（限流/熔断/超并发）会 Inc 对应的 counter，
//     接入 B4 的 metrics 包

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/tgf/metrics"
)

// MethodPolicy 描述一个 RPC 方法的完整策略配置。
// 零值字段表示"不启用对应能力"——例如 MaxConcurrency=0 表示不限并发。
type MethodPolicy struct {
	// Timeout 对应 A7 的超时语义。<=0 时走全局默认（defaultRPCTimeoutNanos）。
	Timeout time.Duration
	// MaxConcurrency 限制同一方法的并发调用数。<=0 表示不限。
	// 实现：计数信号量，超出时快速失败返回 ErrRPCOverload。
	MaxConcurrency int
	// RateLimit 限制同一方法每秒的调用数（QPS）。<=0 表示不限。
	// 实现：令牌桶，桶容量等于速率。
	RateLimit float64
	// CircuitBreaker 熔断器配置，零值表示不启用。
	CircuitBreaker CircuitBreakerConfig
}

// CircuitBreakerConfig 简单的基于计数的断路器：
//   - 连续 FailureThreshold 次失败 → 开路（Open）
//   - Open 状态持续 RecoveryInterval 后进入 HalfOpen
//   - HalfOpen 允许一次探测：成功则回到 Closed，失败则回到 Open
type CircuitBreakerConfig struct {
	FailureThreshold int           // 触发开路的连续失败次数，0 表示不启用
	RecoveryInterval time.Duration // 开路状态持续时长后进入 HalfOpen
}

// ---- 错误值 ----

// ErrRPCOverload 表示因 MaxConcurrency 达上限被拒。
var ErrRPCOverload = errors.New("tgf/rpc: method overload, MaxConcurrency reached")

// ErrRPCRateLimited 表示因 RateLimit 达上限被拒。
var ErrRPCRateLimited = errors.New("tgf/rpc: method rate limited")

// ErrRPCCircuitOpen 表示因断路器打开被拒。
var ErrRPCCircuitOpen = errors.New("tgf/rpc: method circuit breaker open")

// ---- 策略存储 ----

// rpcMethodPolicies 是运行时策略表，key 为 "module.method"，value 为 *methodRuntime。
// sync.Map 保证无锁读优化，配置更新不频繁时性能很好。
var rpcMethodPolicies sync.Map // map[string]*methodRuntime

// methodRuntime 是 MethodPolicy 的运行态：含 policy 配置 + 信号量 + 令牌桶 + 断路器状态。
// 多个 goroutine 共享同一个实例，子结构各自负责自己的并发控制。
type methodRuntime struct {
	policy     MethodPolicy
	sem        chan struct{} // 并发信号量，nil 表示不限并发
	tokenBkt   *tokenBucket  // 令牌桶，nil 表示不限速
	breaker    *circuitState // 断路器，nil 表示不启用
	methodName string        // "module.method" 冗余一份便于 metrics label
}

func newMethodRuntime(name string, p MethodPolicy) *methodRuntime {
	mr := &methodRuntime{policy: p, methodName: name}
	if p.MaxConcurrency > 0 {
		mr.sem = make(chan struct{}, p.MaxConcurrency)
	}
	if p.RateLimit > 0 {
		mr.tokenBkt = newTokenBucket(p.RateLimit, math.Max(1, p.RateLimit))
	}
	if p.CircuitBreaker.FailureThreshold > 0 {
		mr.breaker = &circuitState{
			threshold: p.CircuitBreaker.FailureThreshold,
			recovery:  p.CircuitBreaker.RecoveryInterval,
		}
	}
	return mr
}

// resolveMethodPolicy 返回一个 method 的 methodRuntime。未配置时返回 nil，
// 调用方应判空并走 fast path（只取 timeout）。
func resolveMethodPolicy(module, method string) *methodRuntime {
	if module == "" || method == "" {
		return nil
	}
	key := module + "." + method
	if v, ok := rpcMethodPolicies.Load(key); ok {
		if mr, ok := v.(*methodRuntime); ok {
			return mr
		}
	}
	return nil
}

// SetMethodPolicy 注册/覆盖一个方法的策略。零值字段表示"不启用对应能力"。
// 覆盖会**替换**已有 runtime——老的信号量/令牌桶/断路器状态全部丢弃。
// 生产应当在启动阶段一次性配置，运行时不要频繁切换。
func SetMethodPolicy(method string, p MethodPolicy) {
	if method == "" {
		return
	}
	// 兼容 A7：同步把 Timeout 写到 rpcMethodTimeouts，让 resolveRPCTimeout 的
	// A7 路径也能查到——两条路径同源。
	if p.Timeout > 0 {
		SetMethodRPCTimeout(method, p.Timeout)
	} else {
		SetMethodRPCTimeout(method, 0) // 清除
	}
	rpcMethodPolicies.Store(method, newMethodRuntime(method, p))
}

// ClearMethodPolicies 清空全部策略。仅供测试。
func ClearMethodPolicies() {
	rpcMethodPolicies.Range(func(k, _ any) bool {
		rpcMethodPolicies.Delete(k)
		return true
	})
	ClearMethodRPCTimeoutsForTest()
}

// WithMethodPolicy 是 Server 上的 builder 风格入口。
func (s *Server) WithMethodPolicy(method string, p MethodPolicy) *Server {
	SetMethodPolicy(method, p)
	return s
}

// ---- 策略执行：BeforeCall + release ----

// beforeCall 对一个 methodRuntime 执行策略检查并返回一个 release 闭包。
// 调用方必须在 RPC 调用结束后调 release(err)。release(nil) 表示成功路径。
//
// 检查顺序：熔断器 → 限流 → 并发。理由是"便宜的拒绝"放在前面——熔断器是
// 几个原子 load，限流是一次 mutex，并发是 chan send。依次递增。
//
// E1 修复（HalfOpen 探测名额泄漏）：HalfOpen 状态下 breaker.allow() 经 CAS 消费
// 掉唯一探测名额；原实现里若紧接着被令牌桶/信号量拒绝，名额永不归还——此后
// probeAllow 恒 false，该方法**永久** ErrRPCCircuitOpen，只能重启进程恢复。
// 现在被后续策略拒绝时立即调用 releaseProbe 归还名额，熔断器可自动恢复。
//
// 返回 err != nil 时 release 仍然被调用（内部闭包直接 no-op），调用方不需要
// 特判——但 err 本身不会再被计到断路器，因为是"本地拒绝"而非"远端失败"。
func (mr *methodRuntime) beforeCall() (release func(err error), err error) {
	release = noopRelease

	// 1. 断路器
	var breakerProbe bool
	if mr.breaker != nil {
		ok, probe := mr.breaker.allow()
		if !ok {
			policyCounter("tgf_rpc_policy_reject_circuit_total").Inc()
			return release, ErrRPCCircuitOpen
		}
		breakerProbe = probe
	}

	// 2. 限流
	if mr.tokenBkt != nil && !mr.tokenBkt.allow() {
		if breakerProbe {
			mr.breaker.releaseProbe()
		}
		policyCounter("tgf_rpc_policy_reject_ratelimit_total").Inc()
		return release, ErrRPCRateLimited
	}

	// 3. 并发信号量
	if mr.sem != nil {
		select {
		case mr.sem <- struct{}{}:
		default:
			if breakerProbe {
				mr.breaker.releaseProbe()
			}
			policyCounter("tgf_rpc_policy_reject_concurrency_total").Inc()
			return release, ErrRPCOverload
		}
	}

	// 拼接 release 闭包：反向释放 + 更新断路器
	release = func(callErr error) {
		if mr.sem != nil {
			select {
			case <-mr.sem:
			default:
			}
		}
		if mr.breaker != nil {
			// E1 熔断错误分类：业务错误不计失败（见 isCircuitFailure）。
			if isCircuitFailure(callErr) {
				mr.breaker.markFailure()
			} else {
				mr.breaker.markSuccess()
			}
		}
	}
	return release, nil
}

func noopRelease(_ error) {}

// applyMethodPolicy 是策略管道的统一入口（E1 全覆盖）：所有发送路径——
// SendRPCMessage、网关主链路 sendMessage（doLogic→sendMessage）、
// SendAsyncRPCMessage、SendNoReplyRPCMessage(ByAddress)、SendRPCMessageByStr、
// BorderRPCMessage——共用本函数做限流/熔断/并发检查。
// 未配置策略时只有一次 sync.Map.Load，零分配返回 noopRelease（fast path）。
func applyMethodPolicy(module, method string) (release func(error), err error) {
	if mr := resolveMethodPolicy(module, method); mr != nil {
		return mr.beforeCall()
	}
	return noopRelease, nil
}

// isCircuitFailure 判定一次调用错误是否计入熔断失败（E1 熔断错误分类）。
//
// 分类原则：熔断器隔离的是"节点/链路故障"，不是"业务拒绝"。远端 handler 正常
// 返回的业务 error（参数校验失败、『余额不足』等）说明节点健康，高频业务拒绝
// 不应熔断一个完全健康的服务：
//   - nil → 成功；
//   - rpcx ServiceError（远端 handler 返回值经 rpcx 协议带回，节点收到请求且
//     正常处理）→ 不计失败（按成功处理，同时重置失败计数）；
//   - 其余（tgf.ErrorRPCTimeOut、client.ErrXClientNoServer、连接/网络层错误）
//     → 计失败。
func isCircuitFailure(err error) bool {
	if err == nil {
		return false
	}
	var se client.ServiceError
	if errors.As(err, &se) && se.IsServiceError() {
		return false
	}
	return true
}

// ---- policyCounter：延迟初始化的 metrics counter ----

var (
	policyCounterOnce sync.Once
	policyCounterMap  sync.Map // map[string]metrics.Counter
)

func policyCounter(name string) metrics.Counter {
	if v, ok := policyCounterMap.Load(name); ok {
		return v.(metrics.Counter)
	}
	c := metrics.NewCounter(name, "RPC 方法策略拒绝次数")
	actual, _ := policyCounterMap.LoadOrStore(name, c)
	return actual.(metrics.Counter)
}

// ---- 令牌桶 ----

// tokenBucket 是一个简单的线程安全令牌桶。
// 不是无锁的——每次 allow 都要取锁，但锁住的时间极短，策略热路径能接受。
type tokenBucket struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	capacity float64
	tokens   float64
	last     time.Time
}

func newTokenBucket(rate, capacity float64) *tokenBucket {
	return &tokenBucket{
		rate:     rate,
		capacity: capacity,
		tokens:   capacity, // 启动时桶满
		last:     time.Now(),
	}
}

func (t *tokenBucket) allow() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(t.last).Seconds()
	t.last = now
	t.tokens += elapsed * t.rate
	if t.tokens > t.capacity {
		t.tokens = t.capacity
	}
	if t.tokens >= 1 {
		t.tokens -= 1
		return true
	}
	return false
}

// ---- 断路器 ----

// circuitState 是一个简单的基于计数的断路器：
//   - Closed：正常工作
//   - Open：连续失败达阈值后进入，持续 recovery 时长
//   - HalfOpen：recovery 到期后进入，只允许一次探测，成功→Closed，失败→Open
//
// 所有状态转换走原子操作 + mutex。高频调用场景下性能够用。
type circuitState struct {
	mu           sync.Mutex
	threshold    int
	recovery     time.Duration
	state        int32 // 0=closed, 1=open, 2=halfopen
	failCount    int
	openedAt     time.Time
	halfProbeSet atomic.Bool // true 表示 HalfOpen 的探测已被占用
}

const (
	cbClosed   int32 = 0
	cbOpen     int32 = 1
	cbHalfOpen int32 = 2
)

// allow 返回 (是否允许调用, 本次放行是否消费了 HalfOpen 探测名额)。
// 内部也处理 Open→HalfOpen 的过渡。
// 所有 state 字段读写都走 atomic，避免和 markSuccess/markFailure 的写入 race。
//
// E1：probe 返回值供 beforeCall 在"探测请求被后续策略（限流/并发）拒绝"时
// 调用 releaseProbe 归还名额——否则熔断器永久卡死在 HalfOpen。
func (c *circuitState) allow() (ok bool, probe bool) {
	s := atomic.LoadInt32(&c.state)
	switch s {
	case cbClosed:
		return true, false
	case cbOpen:
		c.mu.Lock()
		// 到期进入 HalfOpen。用 atomic.Store 保证和 allow 的 Load 同步。
		if time.Since(c.openedAt) >= c.recovery {
			atomic.StoreInt32(&c.state, cbHalfOpen)
			c.halfProbeSet.Store(false)
			c.mu.Unlock()
			p := c.probeAllow()
			return p, p
		}
		c.mu.Unlock()
		return false, false
	case cbHalfOpen:
		p := c.probeAllow()
		return p, p
	}
	return true, false
}

// probeAllow 只允许一次探测通过。CAS 保证多 goroutine 并发时只有一个拿到放行。
func (c *circuitState) probeAllow() bool {
	return c.halfProbeSet.CompareAndSwap(false, true)
}

// releaseProbe 归还 HalfOpen 的探测名额（E1 修复）。
// 仅在名额仍被占用时归还（CAS true→false）；探测已完成（markSuccess/markFailure
// 已迁移状态并重置名额）后调用是安全的 no-op。
func (c *circuitState) releaseProbe() {
	c.halfProbeSet.CompareAndSwap(true, false)
}

func (c *circuitState) markSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 任何成功都重置失败计数并回到 Closed
	c.failCount = 0
	atomic.StoreInt32(&c.state, cbClosed)
	c.halfProbeSet.Store(false)
}

func (c *circuitState) markFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// HalfOpen 下的探测失败：立即回到 Open，重置计时
	if atomic.LoadInt32(&c.state) == cbHalfOpen {
		atomic.StoreInt32(&c.state, cbOpen)
		c.openedAt = time.Now()
		c.halfProbeSet.Store(false)
		return
	}
	// Closed 下累计失败
	c.failCount++
	if c.failCount >= c.threshold {
		atomic.StoreInt32(&c.state, cbOpen)
		c.openedAt = time.Now()
	}
}
