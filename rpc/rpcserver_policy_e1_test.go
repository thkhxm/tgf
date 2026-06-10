package rpc

// E1 策略管道修复的回归测试：
//   1. HalfOpen 探测名额被限流/并发拒绝吞掉后必须自动归还（原 P1：永久卡死，
//      只能重启进程或重新 SetMethodPolicy 才能恢复）
//   2. 熔断错误分类——业务错误（rpcx ServiceError）不计失败，避免高频业务拒绝
//      把完全健康的服务熔断
//   3. applyMethodPolicy 统一入口的 fast path 语义

import (
	"errors"
	"testing"
	"time"

	"github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/tgf"
)

// TestCircuitBreaker_HalfOpenProbeReleasedOnRateLimitReject 是 E1 修复的核心回归：
// HalfOpen 探测请求被令牌桶拒绝后，探测名额必须归还——后续调用仍能拿到探测机会，
// 熔断器可以自动恢复（原实现此后所有调用恒 ErrRPCCircuitOpen）。
func TestCircuitBreaker_HalfOpenProbeReleasedOnRateLimitReject(t *testing.T) {
	ClearMethodPolicies()
	defer ClearMethodPolicies()
	// RateLimit=1（桶容量 1）+ 熔断：恢复期遇到一次限流即触发原 bug。
	SetMethodPolicy("mod.e1rl", MethodPolicy{
		RateLimit: 1,
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 1,
			RecoveryInterval: 30 * time.Millisecond,
		},
	})
	mr := resolveMethodPolicy("mod", "e1rl")

	// 1. 一次失败触发 Open（同时消耗掉桶里唯一的令牌）。
	r, err := mr.beforeCall()
	if err != nil {
		t.Fatalf("首次调用应放行, err=%v", err)
	}
	r(errors.New("node down"))

	// 2. 等待 recovery → HalfOpen；此刻令牌桶尚未回填（速率 1/s），
	//    探测请求会先通过熔断器（消费探测名额）再被限流拒绝。
	time.Sleep(50 * time.Millisecond)
	_, err = mr.beforeCall()
	if !errors.Is(err, ErrRPCRateLimited) {
		t.Fatalf("HalfOpen 探测应被限流拒绝, err=%v", err)
	}

	// 3. 原 bug：halfProbeSet 永远为 true → 永久 ErrRPCCircuitOpen。
	//    修复后：名额已归还，令牌回填后探测可以重新进行并恢复 Closed。
	time.Sleep(1100 * time.Millisecond) // 等令牌桶回填 ≥1 个令牌
	r2, err := mr.beforeCall()
	if err != nil {
		t.Fatalf("探测名额应已归还,熔断器应允许新探测, err=%v", err)
	}
	r2(nil) // 探测成功 → Closed

	// 紧接着的调用可能因令牌桶（1 QPS）再次限流——但绝不允许是熔断拒绝：
	// 熔断器已回到 Closed（原 bug 下这里恒为 ErrRPCCircuitOpen）。
	r3, err := mr.beforeCall()
	if errors.Is(err, ErrRPCCircuitOpen) {
		t.Fatalf("熔断器应已恢复 Closed, err=%v", err)
	}
	if err == nil {
		r3(nil)
	}
}

// TestCircuitBreaker_HalfOpenProbeReleasedOnConcurrencyReject 同上，
// 但探测请求被并发信号量拒绝。
func TestCircuitBreaker_HalfOpenProbeReleasedOnConcurrencyReject(t *testing.T) {
	ClearMethodPolicies()
	defer ClearMethodPolicies()
	SetMethodPolicy("mod.e1cc", MethodPolicy{
		MaxConcurrency: 1,
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 1,
			RecoveryInterval: 30 * time.Millisecond,
		},
	})
	mr := resolveMethodPolicy("mod", "e1cc")

	// 一次失败触发 Open。
	holder, err := mr.beforeCall()
	if err != nil {
		t.Fatalf("首次调用应放行, err=%v", err)
	}
	holder(errors.New("node down"))

	// 手工占满唯一并发额度，模拟"恢复期并发打满"
	// （Open 期间 beforeCall 会被熔断器先拒，无法通过正常路径占位）。
	mr.sem <- struct{}{}

	// 等待 recovery → HalfOpen；探测过熔断器（消费名额）后被并发信号量拒绝。
	time.Sleep(50 * time.Millisecond)
	_, err = mr.beforeCall()
	if !errors.Is(err, ErrRPCOverload) {
		t.Fatalf("HalfOpen 探测应被并发拒绝, err=%v", err)
	}

	// 释放并发额度后，探测名额应已归还——熔断器允许新探测并可恢复。
	<-mr.sem
	r, err := mr.beforeCall()
	if err != nil {
		t.Fatalf("探测名额应已归还, err=%v", err)
	}
	r(nil)

	r2, err := mr.beforeCall()
	if err != nil {
		t.Fatalf("探测成功后应回到 Closed, err=%v", err)
	}
	r2(nil)
}

// TestIsCircuitFailure_Classification 表驱动覆盖熔断错误分类规则。
func TestIsCircuitFailure_Classification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil成功", nil, false},
		{"业务错误ServiceError不计失败", client.NewServiceError("余额不足"), false},
		{"RPC超时计失败", tgf.ErrorRPCTimeOut, true},
		{"无可用节点计失败", client.ErrXClientNoServer, true},
		{"普通网络错误计失败", errors.New("connection refused"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isCircuitFailure(c.err); got != c.want {
				t.Errorf("isCircuitFailure(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestCircuitBreaker_BusinessErrorDoesNotOpen 验证错误分类的端到端行为：
// 连续超过 FailureThreshold 次的业务错误（远端 handler 正常返回的 error）
// 不会熔断健康服务；同等次数的网络错误则会。
func TestCircuitBreaker_BusinessErrorDoesNotOpen(t *testing.T) {
	ClearMethodPolicies()
	defer ClearMethodPolicies()
	SetMethodPolicy("mod.e1biz", MethodPolicy{
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 3,
			RecoveryInterval: time.Second,
		},
	})
	mr := resolveMethodPolicy("mod", "e1biz")

	// 6 次业务错误（> 阈值 3）——不应 Open。
	for i := 0; i < 6; i++ {
		r, err := mr.beforeCall()
		if err != nil {
			t.Fatalf("第 %d 次业务错误后熔断器不应打开, err=%v", i, err)
		}
		r(client.NewServiceError("参数校验失败"))
	}

	// 3 次网络错误——应 Open。
	for i := 0; i < 3; i++ {
		r, err := mr.beforeCall()
		if err != nil {
			t.Fatalf("阈值未到不应 Open（第 %d 次）, err=%v", i, err)
		}
		r(tgf.ErrorRPCTimeOut)
	}
	_, err := mr.beforeCall()
	if !errors.Is(err, ErrRPCCircuitOpen) {
		t.Fatalf("连续网络错误达阈值后应 Open, err=%v", err)
	}
}

// TestApplyMethodPolicy_FastPath 验证统一入口：未配置策略返回 noopRelease + nil，
// 已配置则与 beforeCall 行为一致。
func TestApplyMethodPolicy_FastPath(t *testing.T) {
	ClearMethodPolicies()
	defer ClearMethodPolicies()

	release, err := applyMethodPolicy("ghost", "Nope")
	if err != nil {
		t.Fatalf("未配置策略应放行, err=%v", err)
	}
	release(nil) // noopRelease 不应 panic

	SetMethodPolicy("mod.apply", MethodPolicy{MaxConcurrency: 1})
	r1, err := applyMethodPolicy("mod", "apply")
	if err != nil {
		t.Fatalf("首个并发应放行, err=%v", err)
	}
	_, err = applyMethodPolicy("mod", "apply")
	if !errors.Is(err, ErrRPCOverload) {
		t.Fatalf("超并发应拒绝, err=%v", err)
	}
	r1(nil)
}
