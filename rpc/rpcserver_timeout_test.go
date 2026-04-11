package rpc

// A7 单测：RPC 超时策略的解析优先级和 builder Option 行为。
// 不涉及真实 rpcx 调用——只测 resolveRPCTimeout + Set/With* 的配置状态。

import (
	"sync"
	"testing"
	"time"
)

// resetTimeoutState 把 package-level 超时状态恢复到 init 时的默认值。
// 每个测试首尾都调一次，避免测试间互相污染（测试并行时顺序不确定）。
func resetTimeoutState(t *testing.T) {
	t.Helper()
	SetDefaultRPCTimeout(5 * time.Second)
	ClearMethodRPCTimeoutsForTest()
}

func TestResolveRPCTimeout_DefaultFallback(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	// 没注册 per-method 时返回全局默认 5s
	if got := resolveRPCTimeout("mod", "SomeMethod"); got != 5*time.Second {
		t.Errorf("default = %v, want 5s", got)
	}
	// 空 moduleName / methodName 也走默认
	if got := resolveRPCTimeout("", ""); got != 5*time.Second {
		t.Errorf("empty names: %v, want 5s", got)
	}
	if got := resolveRPCTimeout("only-mod", ""); got != 5*time.Second {
		t.Errorf("empty method: %v, want 5s", got)
	}
}

func TestResolveRPCTimeout_MethodOverride(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	SetMethodRPCTimeout("gate.Login", 12*time.Second)
	if got := resolveRPCTimeout("gate", "Login"); got != 12*time.Second {
		t.Errorf("override for gate.Login = %v, want 12s", got)
	}
	// 未覆盖的方法仍走默认
	if got := resolveRPCTimeout("gate", "Offline"); got != 5*time.Second {
		t.Errorf("non-override: %v, want 5s", got)
	}
	// 另一个模块同名方法不共享覆盖
	if got := resolveRPCTimeout("other", "Login"); got != 5*time.Second {
		t.Errorf("other.Login: %v, want 5s", got)
	}
}

func TestSetDefaultRPCTimeout_Effective(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	SetDefaultRPCTimeout(2 * time.Second)
	if got := resolveRPCTimeout("any", "method"); got != 2*time.Second {
		t.Errorf("default after Set = %v, want 2s", got)
	}
}

func TestSetDefaultRPCTimeout_IgnoresNonPositive(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	SetDefaultRPCTimeout(7 * time.Second)
	SetDefaultRPCTimeout(0)       // 应被忽略
	SetDefaultRPCTimeout(-1)      // 应被忽略
	if got := resolveRPCTimeout("a", "b"); got != 7*time.Second {
		t.Errorf("default = %v, want 7s (non-positive should be ignored)", got)
	}
}

func TestResolveRPCTimeout_DefensiveAgainstCorruptedDefault(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	// 直接写 atomic 模拟被破坏的状态（SetDefaultRPCTimeout 会拒绝 <= 0，
	// 但防御代码应当对任意被改坏的值仍返回 >0 的超时）
	defaultRPCTimeoutNanos.Store(0)
	got := resolveRPCTimeout("a", "b")
	if got <= 0 {
		t.Errorf("resolved timeout must be > 0 even when default is corrupted, got %v", got)
	}
}

func TestSetMethodRPCTimeout_DeletesOnNonPositive(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	SetMethodRPCTimeout("gate.Login", 10*time.Second)
	if got := resolveRPCTimeout("gate", "Login"); got != 10*time.Second {
		t.Fatalf("pre-delete: %v, want 10s", got)
	}

	// 传 0 应当删除覆盖
	SetMethodRPCTimeout("gate.Login", 0)
	if got := resolveRPCTimeout("gate", "Login"); got != 5*time.Second {
		t.Errorf("after delete: %v, want 5s (fall back to default)", got)
	}
}

func TestWithDefaultRPCTimeout_Chainable(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	s := NewRPCServer()
	returned := s.WithDefaultRPCTimeout(8 * time.Second)
	if returned != s {
		t.Errorf("WithDefaultRPCTimeout should return the same *Server for chaining")
	}
	if got := resolveRPCTimeout("x", "y"); got != 8*time.Second {
		t.Errorf("default after With = %v, want 8s", got)
	}
}

func TestWithMethodTimeout_Chainable(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	s := NewRPCServer()
	returned := s.WithMethodTimeout("svc.Foo", 3*time.Second)
	if returned != s {
		t.Errorf("WithMethodTimeout should return the same *Server")
	}
	if got := resolveRPCTimeout("svc", "Foo"); got != 3*time.Second {
		t.Errorf("override after With = %v, want 3s", got)
	}
}

func TestResolveRPCTimeout_ConcurrentReadWrite(t *testing.T) {
	resetTimeoutState(t)
	defer resetTimeoutState(t)

	// 并发读和写不应触发 race detector（由 sync.Map + atomic.Int64 保证）。
	const N = 64
	var wg sync.WaitGroup
	wg.Add(N * 2)

	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			SetMethodRPCTimeout("mod.Method", 7*time.Second)
		}()
		go func() {
			defer wg.Done()
			_ = resolveRPCTimeout("mod", "Method")
		}()
	}
	wg.Wait()
}
