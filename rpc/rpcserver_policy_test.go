package rpc

// C6 · RPC 策略化完整版单测
//
// 覆盖：
//   1. policy 查找 — 未注册返回 nil，注册后能查到
//   2. MaxConcurrency — 超并发返回 ErrRPCOverload
//   3. RateLimit — 令牌桶速率控制
//   4. CircuitBreaker — Closed → Open → HalfOpen → Closed 状态机
//   5. SetMethodPolicy 同时刷新 A7 的 timeout 表
//   6. release 闭包在成功/失败路径都正确处理
//   7. metrics 拒绝路径的 counter 触发

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thkhxm/tgf/v2/metrics"
)

func TestResolveMethodPolicy_MissingReturnsNil(t *testing.T) {
	ClearMethodPolicies()
	if mr := resolveMethodPolicy("mod", "method"); mr != nil {
		t.Error("未注册的方法应返回 nil")
	}
}

func TestSetMethodPolicy_StoresRuntime(t *testing.T) {
	ClearMethodPolicies()
	SetMethodPolicy("mod.m1", MethodPolicy{
		Timeout:        3 * time.Second,
		MaxConcurrency: 5,
	})
	defer ClearMethodPolicies()

	mr := resolveMethodPolicy("mod", "m1")
	if mr == nil {
		t.Fatal("注册后应查到 runtime")
	}
	if mr.policy.Timeout != 3*time.Second {
		t.Errorf("Timeout 期望 3s, 实际 %v", mr.policy.Timeout)
	}
	// A7 超时表也应当有同步写入
	if resolveRPCTimeout("mod", "m1") != 3*time.Second {
		t.Error("SetMethodPolicy 应当同步写 A7 timeout 表")
	}
}

func TestSetMethodPolicy_EmptyMethodIsNoop(t *testing.T) {
	ClearMethodPolicies()
	SetMethodPolicy("", MethodPolicy{Timeout: time.Second})
	if resolveMethodPolicy("", "m") != nil {
		t.Error("空 method 应当 no-op")
	}
}

// ---- MaxConcurrency ----

func TestMaxConcurrency_RejectsOverLimit(t *testing.T) {
	ClearMethodPolicies()
	SetMethodPolicy("mod.cc", MethodPolicy{MaxConcurrency: 2})
	defer ClearMethodPolicies()

	mr := resolveMethodPolicy("mod", "cc")
	r1, err := mr.beforeCall()
	if err != nil {
		t.Fatal(err)
	}
	r2, err := mr.beforeCall()
	if err != nil {
		t.Fatal(err)
	}
	// 第三个应当被拒
	r3, err := mr.beforeCall()
	if !errors.Is(err, ErrRPCOverload) {
		t.Errorf("第三个应返回 ErrRPCOverload, 实际 %v", err)
	}
	r3(err) // no-op

	// 释放一个，第四个应当成功
	r1(nil)
	r4, err := mr.beforeCall()
	if err != nil {
		t.Errorf("释放后应可再取 slot, 实际 %v", err)
	}
	r4(nil)
	r2(nil)
}

// ---- RateLimit ----

func TestRateLimit_RejectsExcess(t *testing.T) {
	ClearMethodPolicies()
	// 容量 3，速率 3/s——首 3 次成功，第 4 次立即拒
	SetMethodPolicy("mod.rl", MethodPolicy{RateLimit: 3})
	defer ClearMethodPolicies()

	mr := resolveMethodPolicy("mod", "rl")
	for i := 0; i < 3; i++ {
		r, err := mr.beforeCall()
		if err != nil {
			t.Fatalf("第 %d 次应通过, 实际 %v", i, err)
		}
		r(nil)
	}
	r, err := mr.beforeCall()
	if !errors.Is(err, ErrRPCRateLimited) {
		t.Errorf("第 4 次应 ErrRPCRateLimited, 实际 %v", err)
	}
	r(err)
}

func TestRateLimit_RefillsOverTime(t *testing.T) {
	ClearMethodPolicies()
	// 容量 2，速率 100/s → 每 10ms 1 个 token
	SetMethodPolicy("mod.rl2", MethodPolicy{RateLimit: 100})
	defer ClearMethodPolicies()

	mr := resolveMethodPolicy("mod", "rl2")
	// 先耗光当前令牌
	for i := 0; i < 100; i++ {
		_, _ = mr.beforeCall()
	}
	// 等 50ms 应该能重新拿到令牌
	time.Sleep(50 * time.Millisecond)
	r, err := mr.beforeCall()
	if err != nil {
		t.Errorf("50ms 等待后应重新可用, 实际 %v", err)
	}
	r(nil)
}

// ---- CircuitBreaker ----

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	ClearMethodPolicies()
	SetMethodPolicy("mod.cb", MethodPolicy{
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 3,
			RecoveryInterval: 50 * time.Millisecond,
		},
	})
	defer ClearMethodPolicies()

	mr := resolveMethodPolicy("mod", "cb")
	for i := 0; i < 3; i++ {
		r, err := mr.beforeCall()
		if err != nil {
			t.Fatalf("closed 状态第 %d 次应通过, 实际 %v", i, err)
		}
		r(errors.New("boom"))
	}
	// 阈值达到后应当 Open
	r, err := mr.beforeCall()
	if !errors.Is(err, ErrRPCCircuitOpen) {
		t.Errorf("阈值后应 Open, 实际 %v", err)
	}
	r(err)

	// 等待 recovery → HalfOpen
	time.Sleep(80 * time.Millisecond)
	r2, err := mr.beforeCall()
	if err != nil {
		t.Errorf("recovery 后应允许一次探测, 实际 %v", err)
	}
	// 探测成功 → 回到 Closed
	r2(nil)

	// Closed 状态应当再次允许调用
	r3, err := mr.beforeCall()
	if err != nil {
		t.Errorf("探测成功后应回到 Closed, 实际 %v", err)
	}
	r3(nil)
}

func TestCircuitBreaker_HalfOpenFailReopens(t *testing.T) {
	ClearMethodPolicies()
	SetMethodPolicy("mod.cb2", MethodPolicy{
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 2,
			RecoveryInterval: 30 * time.Millisecond,
		},
	})
	defer ClearMethodPolicies()

	mr := resolveMethodPolicy("mod", "cb2")
	// 触发 Open
	for i := 0; i < 2; i++ {
		r, _ := mr.beforeCall()
		r(errors.New("boom"))
	}
	// 等 recovery → HalfOpen 允许一次探测
	time.Sleep(50 * time.Millisecond)
	r, err := mr.beforeCall()
	if err != nil {
		t.Fatalf("HalfOpen 探测应放行, 实际 %v", err)
	}
	// 探测失败 → 立即回到 Open
	r(errors.New("still bad"))

	// 立即再调应当被拒
	r2, err := mr.beforeCall()
	if !errors.Is(err, ErrRPCCircuitOpen) {
		t.Errorf("探测失败后应立即回到 Open, 实际 %v", err)
	}
	r2(err)
}

func TestCircuitBreaker_SuccessResetsFailCount(t *testing.T) {
	ClearMethodPolicies()
	SetMethodPolicy("mod.cb3", MethodPolicy{
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 3,
			RecoveryInterval: 1 * time.Second,
		},
	})
	defer ClearMethodPolicies()

	mr := resolveMethodPolicy("mod", "cb3")
	// 失败 2 次（未到阈值）
	for i := 0; i < 2; i++ {
		r, _ := mr.beforeCall()
		r(errors.New("boom"))
	}
	// 成功一次应重置计数器
	r, _ := mr.beforeCall()
	r(nil)
	// 再失败 2 次——因为计数器被重置，不应到阈值
	for i := 0; i < 2; i++ {
		r, err := mr.beforeCall()
		if err != nil {
			t.Fatalf("阈值未到 (count=2 after reset) 不应 Open, 实际 %v", err)
		}
		r(errors.New("boom"))
	}
}

// ---- 组合场景 ----

func TestBeforeCall_ConcurrentSafe(t *testing.T) {
	ClearMethodPolicies()
	SetMethodPolicy("mod.concurrent", MethodPolicy{
		MaxConcurrency: 10,
		RateLimit:      1000,
		CircuitBreaker: CircuitBreakerConfig{FailureThreshold: 100, RecoveryInterval: time.Second},
	})
	defer ClearMethodPolicies()

	mr := resolveMethodPolicy("mod", "concurrent")
	var (
		wg       sync.WaitGroup
		pass     atomic.Int32
		rejected atomic.Int32
	)
	const N = 50
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			r, err := mr.beforeCall()
			if err != nil {
				rejected.Add(1)
			} else {
				pass.Add(1)
				time.Sleep(5 * time.Millisecond)
				r(nil)
			}
		}()
	}
	wg.Wait()
	if pass.Load()+rejected.Load() != N {
		t.Errorf("pass+rejected 应为 %d, 实际 %d", N, pass.Load()+rejected.Load())
	}
}

// ---- metrics 集成 ----

func TestPolicyReject_IncrementsMetrics(t *testing.T) {
	ClearMethodPolicies()
	// 替换 provider 让计数可观察
	m := metrics.NewMemoryProvider()
	defer metrics.SetProvider(metrics.NoopProvider())
	metrics.SetProvider(m)
	// 清掉 LoadOrStore 里之前用 NoopProvider 创建的 counter 缓存
	policyCounterMap = sync.Map{}

	// 容量 1，立即用满，再次调用拒绝
	SetMethodPolicy("mod.rm", MethodPolicy{MaxConcurrency: 1})
	defer ClearMethodPolicies()
	mr := resolveMethodPolicy("mod", "rm")
	r1, _ := mr.beforeCall()
	_, err := mr.beforeCall()
	if !errors.Is(err, ErrRPCOverload) {
		t.Fatalf("应 ErrRPCOverload, 实际 %v", err)
	}
	r1(nil)

	if v := m.CounterValue("tgf_rpc_policy_reject_concurrency_total"); v != 1 {
		t.Errorf("concurrency reject counter 期望 1, 实际 %v", v)
	}
}

// ---- WithMethodPolicy builder ----

func TestWithMethodPolicy_BuilderForm(t *testing.T) {
	ClearMethodPolicies()
	defer ClearMethodPolicies()

	s := newBareServer()
	got := s.WithMethodPolicy("mod.wm", MethodPolicy{Timeout: 2 * time.Second})
	if got != s {
		t.Error("WithMethodPolicy 应当返回 *Server 自己")
	}
	if resolveRPCTimeout("mod", "wm") != 2*time.Second {
		t.Error("builder 方法应生效")
	}
}
