package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F3 · 登录锁 watchdog 续期与"锁丢失可感知"测试
//
// 对应审计"登录锁 5s TTL 无续期，锁内关键路径最坏可超 TTL；锁过期/释放失败
// 均无感知"：
//   - watchdog 周期性 Refresh（refreshFn/releaseFn 注入 fake，无需真实 Redis；
//     真实 Redis 的续期行为见 it_login_lock_test.go 集成用例）；
//   - 续期失败 / 释放时 ErrLockNotHeld → tgf_gate_login_lock_lost_total 计数器
//     累加 + Warn 日志，锁过期从"静默"变为可观测事件；
//   - release 幂等且会等 watchdog 真正退出。
//
//2026/6/10
//***************************************************

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bsm/redislock"
	"github.com/thkhxm/tgf/metrics"
)

// newF3WatchdogHandle 构造一个注入 fake refresh/release 的锁句柄（不依赖真实 Redis）。
func newF3WatchdogHandle(refresh func(ctx context.Context, ttl time.Duration) error,
	release func(ctx context.Context) error) *redislockHandle {
	return &redislockHandle{
		key:       "tgf:gate:login:lock:f3-watchdog",
		refreshFn: refresh,
		releaseFn: release,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// withF3ShortRefreshInterval 缩短续期周期便于测试。
func withF3ShortRefreshInterval(t *testing.T, d time.Duration) func() {
	t.Helper()
	orig := loginLockRefreshInterval
	loginLockRefreshInterval = d
	return func() { loginLockRefreshInterval = orig }
}

// withF3MemoryMetrics 切换内存 Provider 并重绑 F3 锁计数器，返回 (provider, 恢复函数)。
func withF3MemoryMetrics(t *testing.T) (interface{ CounterValue(string) float64 }, func()) {
	t.Helper()
	orig := metrics.GetProvider()
	mp := metrics.NewMemoryProvider()
	metrics.SetProvider(mp)
	resetLoginLockMetricsOnceForTest()
	return mp, func() {
		metrics.SetProvider(orig)
		resetLoginLockMetricsOnceForTest()
	}
}

// TestF3_LockWatchdog_RefreshesPeriodically 验证持锁期间 watchdog 周期续期，
// release 后停止续期且 releaseFn 恰好执行一次（幂等）。
func TestF3_LockWatchdog_RefreshesPeriodically(t *testing.T) {
	defer withF3ShortRefreshInterval(t, 10*time.Millisecond)()

	var refreshCount, releaseCount atomic.Int32
	h := newF3WatchdogHandle(
		func(ctx context.Context, ttl time.Duration) error {
			if ttl != loginLockTTL {
				t.Errorf("Refresh ttl = %v, want %v(与 db 层 TTL 一致)", ttl, loginLockTTL)
			}
			refreshCount.Add(1)
			return nil
		},
		func(ctx context.Context) error {
			releaseCount.Add(1)
			return nil
		},
	)
	go h.watchdog()

	// 等若干个续期周期
	deadline := time.Now().Add(2 * time.Second)
	for refreshCount.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := refreshCount.Load(); got < 3 {
		t.Fatalf("watchdog 应周期续期, 2s 内仅 %d 次", got)
	}

	h.release()
	select {
	case <-h.done:
	case <-time.After(time.Second):
		t.Fatalf("release 后 watchdog 未退出")
	}
	after := refreshCount.Load()
	time.Sleep(50 * time.Millisecond)
	if refreshCount.Load() != after {
		t.Fatalf("release 后不应再续期")
	}

	// 释放幂等
	h.release()
	h.release()
	if releaseCount.Load() != 1 {
		t.Fatalf("releaseFn 应恰好执行一次, got %d", releaseCount.Load())
	}
}

// TestF3_LockWatchdog_LostLockObservable 验证审计要求的"锁过期/释放失败可感知"：
//  1. 续期失败（锁已过期被抢占）→ watchdog 退出 + 计数器 +1；
//  2. 释放时 ErrLockNotHeld → 计数器再 +1。
func TestF3_LockWatchdog_LostLockObservable(t *testing.T) {
	defer withF3ShortRefreshInterval(t, 10*time.Millisecond)()
	mp, restore := withF3MemoryMetrics(t)
	defer restore()

	h := newF3WatchdogHandle(
		func(ctx context.Context, ttl time.Duration) error {
			return redislock.ErrNotObtained // 模拟锁已过期,续期失败
		},
		func(ctx context.Context) error {
			return redislock.ErrLockNotHeld // 模拟释放时锁已不被持有
		},
	)
	go h.watchdog()

	// 续期失败 → watchdog 自行退出
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("续期失败后 watchdog 未退出")
	}
	if got := mp.CounterValue("tgf_gate_login_lock_lost_total"); got != 1 {
		t.Fatalf("续期失败应计 1 次锁丢失, got %v", got)
	}

	// 释放时 ErrLockNotHeld → 再计 1 次
	h.release()
	if got := mp.CounterValue("tgf_gate_login_lock_lost_total"); got != 2 {
		t.Fatalf("释放未持有应再计 1 次锁丢失, got %v", got)
	}
}

// TestF3_LockRelease_OtherErrorNotCountedAsLost 验证一般释放错误（如网络抖动）
// 只告警不计入"锁丢失"——计数器语义保持精确（仅 TTL 过期/被抢占）。
func TestF3_LockRelease_OtherErrorNotCountedAsLost(t *testing.T) {
	defer withF3ShortRefreshInterval(t, 10*time.Millisecond)()
	mp, restore := withF3MemoryMetrics(t)
	defer restore()

	h := newF3WatchdogHandle(
		func(ctx context.Context, ttl time.Duration) error { return nil },
		func(ctx context.Context) error { return errors.New("network jitter") },
	)
	go h.watchdog()
	h.release()

	if got := mp.CounterValue("tgf_gate_login_lock_lost_total"); got != 0 {
		t.Fatalf("一般释放错误不应计入锁丢失, got %v", got)
	}
}
