package util

// A5 单测：pool.Go 的池满 fallback 行为 + GoE 的 error 返回。
//
// 白盒测试：直接操作 package-level goroutinePool。测试里构造一个容量 1 的临时
// pool 替代全局的 goroutinePool，然后测试 fallback 语义。测完恢复原 pool。

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
)

// withTinyPool 把 goroutinePool 替换为容量 capacity 的临时 pool，返回恢复函数。
// 测完必须调恢复函数，否则会影响后续测试对 package-level pool 的依赖。
func withTinyPool(t *testing.T, capacity int) func() {
	t.Helper()
	orig := goroutinePool
	p, err := ants.NewPool(capacity, ants.WithNonblocking(true))
	if err != nil {
		t.Fatalf("create tiny pool: %v", err)
	}
	goroutinePool = p
	return func() {
		goroutinePool = orig
		p.Release()
	}
}

// TestGo_HappyPath 快路径：任务被池接受并执行。
func TestGo_HappyPath(t *testing.T) {
	defer withTinyPool(t, 4)()

	var done atomic.Bool
	wg := sync.WaitGroup{}
	wg.Add(1)
	Go(func() {
		defer wg.Done()
		done.Store(true)
	})

	waitTimeout(t, &wg, 2*time.Second, "task should run")
	if !done.Load() {
		t.Errorf("task did not execute")
	}
}

// TestGo_NilTaskIsNoop 验证传 nil 不会 panic。
func TestGo_NilTaskIsNoop(t *testing.T) {
	defer withTinyPool(t, 2)()
	Go(nil) // 不应 panic
}

// TestGo_PoolFullFallbackToBareGoroutine 关键测试：构造一个容量 1 的 nonblocking
// pool，先把它塞住，然后 Go 一个新任务——它应当通过裸 goroutine fallback 执行，
// 而不是被丢弃或阻塞。
func TestGo_PoolFullFallbackToBareGoroutine(t *testing.T) {
	defer withTinyPool(t, 1)()

	// 第一个任务占住 pool
	block := make(chan struct{})
	var firstStarted atomic.Bool
	Go(func() {
		firstStarted.Store(true)
		<-block // 阻塞直到测试结束
	})
	// 等第一个任务真的开始，保证 pool 确实被占
	for i := 0; i < 100 && !firstStarted.Load(); i++ {
		time.Sleep(time.Millisecond)
	}
	if !firstStarted.Load() {
		t.Fatalf("first task did not start; pool misbehaving")
	}

	// 第二个任务——此时 pool 满，原实现会静默丢失，A5 fallback 会走裸 goroutine
	var secondRan atomic.Bool
	wg := sync.WaitGroup{}
	wg.Add(1)
	Go(func() {
		defer wg.Done()
		secondRan.Store(true)
	})

	waitTimeout(t, &wg, 2*time.Second, "second task should run via fallback")
	if !secondRan.Load() {
		t.Errorf("second task was dropped (A5 fallback broken)")
	}

	// 释放第一个任务
	close(block)
}

// TestGoE_PropagatesOverloadError 验证 GoE 在池满时返回 ants.ErrPoolOverload，
// 调用方可以自己决定怎么处理。
func TestGoE_PropagatesOverloadError(t *testing.T) {
	defer withTinyPool(t, 1)()

	block := make(chan struct{})
	var firstStarted atomic.Bool
	if err := GoE(func() {
		firstStarted.Store(true)
		<-block
	}); err != nil {
		t.Fatalf("first GoE should succeed: %v", err)
	}
	for i := 0; i < 100 && !firstStarted.Load(); i++ {
		time.Sleep(time.Millisecond)
	}

	err := GoE(func() {})
	if err == nil {
		t.Errorf("GoE should return error when pool full")
	}

	close(block)
}

// TestGoE_NilTask 验证 GoE 传 nil 返回 nil error，不 panic。
func TestGoE_NilTask(t *testing.T) {
	defer withTinyPool(t, 2)()
	if err := GoE(nil); err != nil {
		t.Errorf("GoE(nil) err = %v, want nil", err)
	}
}

// waitTimeout 是测试小辅助：在指定时间内等 WaitGroup 完成，超时则 t.Fatalf。
func waitTimeout(t *testing.T, wg *sync.WaitGroup, d time.Duration, msg string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("timeout waiting: %s", msg)
	}
}
