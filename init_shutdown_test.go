// Package tgf
// @Description: D3 / P0-3 优雅停机编排单测
//
// 说明：Windows 上无法以编程方式向自身投递 SIGTERM/os.Interrupt
// （signal 包文档明确 "Interrupt is not implemented on windows"），
// 因此这里不做"真实信号端到端"用例，而是：
//  1. 直接断言注册的信号集合（SIGTERM 回归点，os.Kill 死代码回归点）；
//  2. 把信号到来之后的编排函数 gracefulShutdown / runShutdownSequence
//     作为单元直接驱动，覆盖顺序、panic 隔离、超时兜底与二次信号强退。
package tgf

import (
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// withCleanShutdownState 隔离全局停机注册表，避免测试间互相污染。
func withCleanShutdownState(t *testing.T) {
	t.Helper()
	destroyMu.Lock()
	oldHandlers := destroyList
	oldHooks := finalFlushHooks
	destroyList = nil
	finalFlushHooks = nil
	destroyMu.Unlock()
	t.Cleanup(func() {
		destroyMu.Lock()
		destroyList = oldHandlers
		finalFlushHooks = oldHooks
		destroyMu.Unlock()
	})
}

// recordingHandler 把 Destroy 调用按序记录进共享 slice；可选阻塞 / panic。
type recordingHandler struct {
	name     string
	mu       *sync.Mutex
	order    *[]string
	block    chan struct{} // 非 nil 时 Destroy 一直阻塞到该 chan 关闭（模拟卡死）
	panicMsg string        // 非空时记录之后 panic（模拟业务 Destroy 崩溃）
}

func (h *recordingHandler) Destroy() {
	if h.block != nil {
		<-h.block
	}
	h.mu.Lock()
	*h.order = append(*h.order, h.name)
	h.mu.Unlock()
	if h.panicMsg != "" {
		panic(h.panicMsg)
	}
}

// TestShutdownSignals_ContainSIGTERMAndInterrupt 是 P0-3 的核心回归点：
// 容器/K8s 滚动发布发送 SIGTERM，必须注册；os.Kill（SIGKILL）不可捕获，
// 注册了也是死代码，必须移除。
func TestShutdownSignals_ContainSIGTERMAndInterrupt(t *testing.T) {
	var hasTerm, hasInt, hasKill bool
	for _, s := range shutdownSignals {
		switch s {
		case syscall.SIGTERM:
			hasTerm = true
		case os.Interrupt:
			hasInt = true
		case os.Kill:
			hasKill = true
		}
	}
	if !hasTerm {
		t.Error("shutdownSignals 缺少 syscall.SIGTERM——容器滚动发布将不会触发优雅停机(P0-3 回归)")
	}
	if !hasInt {
		t.Error("shutdownSignals 缺少 os.Interrupt——本地 Ctrl-C 将不会触发优雅停机")
	}
	if hasKill {
		t.Error("shutdownSignals 不应包含 os.Kill——SIGKILL 不可捕获,注册是死代码")
	}
}

// TestRunShutdownSequence_OrderAndPanicIsolation 验证：
//  1. handler 按注册顺序执行；
//  2. 单个 handler panic 不会跳过后续 handler；
//  3. 终末 flush 钩子在所有 handler 之后执行，且钩子 panic 不中断后续钩子。
func TestRunShutdownSequence_OrderAndPanicIsolation(t *testing.T) {
	withCleanShutdownState(t)
	var mu sync.Mutex
	order := make([]string, 0, 8)

	AddDestroyHandler(&recordingHandler{name: "h1", mu: &mu, order: &order, panicMsg: "h1 boom"})
	AddDestroyHandler(&recordingHandler{name: "h2", mu: &mu, order: &order})
	RegisterFinalFlushHook(func() {
		mu.Lock()
		order = append(order, "flush1")
		mu.Unlock()
		panic("flush1 boom")
	})
	RegisterFinalFlushHook(func() {
		mu.Lock()
		order = append(order, "flush2")
		mu.Unlock()
	})

	runShutdownSequence()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"h1", "h2", "flush1", "flush2"}
	if len(order) != len(want) {
		t.Fatalf("执行序列 = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("执行序列第 %d 步 = %v, want %v (完整序列 %v)", i, order[i], want[i], order)
		}
	}
}

// TestAddDestroyHandler_NilSafe 验证 nil handler / nil 钩子被忽略，不会在停机时 panic。
func TestAddDestroyHandler_NilSafe(t *testing.T) {
	withCleanShutdownState(t)
	AddDestroyHandler(nil)
	RegisterFinalFlushHook(nil)
	if got := FinalFlushHookCount(); got != 0 {
		t.Errorf("FinalFlushHookCount = %v, want 0 (nil 钩子应被忽略)", got)
	}
	// 不应 panic
	runShutdownSequence()
}

// TestGracefulShutdown_CompletesAndNotifies 验证优雅完成路径：
// 信号到来后 handler 与 flush 钩子全部执行 → 通知 CloseChan → 不调用 exit。
func TestGracefulShutdown_CompletesAndNotifies(t *testing.T) {
	withCleanShutdownState(t)
	var mu sync.Mutex
	order := make([]string, 0, 4)
	var flushCalled atomic.Bool

	AddDestroyHandler(&recordingHandler{name: "h1", mu: &mu, order: &order})
	RegisterFinalFlushHook(func() { flushCalled.Store(true) })

	notify := make(chan bool, 1)
	sigCh := make(chan os.Signal, 1)
	exitCode := int64(-1)
	gracefulShutdown(sigCh, 5*time.Second, func(code int) {
		atomic.StoreInt64(&exitCode, int64(code))
	}, notify)

	select {
	case <-notify:
		// 预期：优雅完成后通知主线退出
	default:
		t.Fatal("优雅停机完成后未向 CloseChan 发送通知,主线 <-Run() 将永远阻塞")
	}
	if !flushCalled.Load() {
		t.Error("信号触发后终末 flush 钩子未被调用(P0-3 验收点)")
	}
	mu.Lock()
	handlerRan := len(order) == 1 && order[0] == "h1"
	mu.Unlock()
	if !handlerRan {
		t.Errorf("destroy handler 未按预期执行, order = %v", order)
	}
	if got := atomic.LoadInt64(&exitCode); got != -1 {
		t.Errorf("优雅完成路径不应调用 exit, 但被以 code=%v 调用", got)
	}
}

// TestGracefulShutdown_TimeoutForcesNonZeroExit 验证看门狗兜底：
// 停机序列卡死时，超时后以非零码强制退出，且不向 CloseChan 发优雅完成通知。
func TestGracefulShutdown_TimeoutForcesNonZeroExit(t *testing.T) {
	withCleanShutdownState(t)
	var mu sync.Mutex
	order := make([]string, 0, 1)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) }) // 释放被卡住的 goroutine

	AddDestroyHandler(&recordingHandler{name: "stuck", mu: &mu, order: &order, block: block})

	notify := make(chan bool, 1)
	exitCode := int64(-1)
	start := time.Now()
	gracefulShutdown(make(chan os.Signal), 50*time.Millisecond, func(code int) {
		atomic.StoreInt64(&exitCode, int64(code))
	}, notify)
	elapsed := time.Since(start)

	if got := atomic.LoadInt64(&exitCode); got != exitCodeShutdownTimeout {
		t.Errorf("看门狗超时后 exit code = %v, want %v", got, exitCodeShutdownTimeout)
	}
	if elapsed > 3*time.Second {
		t.Errorf("看门狗未生效,阻塞了 %v", elapsed)
	}
	select {
	case <-notify:
		t.Error("超时强杀路径不应向 CloseChan 发送优雅完成通知")
	default:
	}
}

// TestGracefulShutdown_SecondSignalForcesExit 验证停机期间收到第二次信号立即强退。
func TestGracefulShutdown_SecondSignalForcesExit(t *testing.T) {
	withCleanShutdownState(t)
	var mu sync.Mutex
	order := make([]string, 0, 1)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	AddDestroyHandler(&recordingHandler{name: "stuck", mu: &mu, order: &order, block: block})

	sigCh := make(chan os.Signal, 1)
	sigCh <- syscall.SIGTERM // 预置"第二次信号"
	notify := make(chan bool, 1)
	exitCode := int64(-1)
	gracefulShutdown(sigCh, time.Minute, func(code int) {
		atomic.StoreInt64(&exitCode, int64(code))
	}, notify)

	if got := atomic.LoadInt64(&exitCode); got != exitCodeForcedExit {
		t.Errorf("第二次信号后 exit code = %v, want %v", got, exitCodeForcedExit)
	}
}

// TestSetShutdownTimeout 验证看门狗超时的配置入口与非法值保护。
func TestSetShutdownTimeout(t *testing.T) {
	old := getShutdownTimeout()
	t.Cleanup(func() { SetShutdownTimeout(old) })

	SetShutdownTimeout(123 * time.Second)
	if got := getShutdownTimeout(); got != 123*time.Second {
		t.Errorf("SetShutdownTimeout 后 getShutdownTimeout = %v, want 123s", got)
	}
	SetShutdownTimeout(0)
	if got := getShutdownTimeout(); got != 123*time.Second {
		t.Errorf("SetShutdownTimeout(0) 应被忽略, 但超时变成了 %v", got)
	}
	SetShutdownTimeout(-time.Second)
	if got := getShutdownTimeout(); got != 123*time.Second {
		t.Errorf("SetShutdownTimeout(负数) 应被忽略, 但超时变成了 %v", got)
	}
}
