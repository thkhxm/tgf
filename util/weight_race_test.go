package util

// A5 单测：weight.Hit 的并发原子性。
//
// 白盒测试（package util 内），直接访问 weight[T] 结构体和 atomic.Int32 字段。
// 原实现的 `w.amount--` 在高并发下会丢 Hit 或出现负数；A5 的 CAS 循环保证每次
// 成功的 Hit 恰好消耗 1 份库存。

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestWeightItemHit_Sequential 先做基础行为测试：顺序 Hit 应当精确消耗 amount 次后返回 (nil, false)。
func TestWeightItemHit_Sequential(t *testing.T) {
	item := &weight[int]{ratio: 10, data: 42}
	item.amount.Store(3)

	// 3 次 Hit 应当都成功
	for i := 0; i < 3; i++ {
		it, done := item.Hit()
		if it == nil {
			t.Fatalf("Hit %d: item returned nil unexpectedly", i)
		}
		wantDone := i == 2
		if done != wantDone {
			t.Errorf("Hit %d: done = %v, want %v", i, done, wantDone)
		}
	}

	// 第 4 次应当 (nil, false)
	it, done := item.Hit()
	if it != nil || done {
		t.Errorf("Hit after exhausted = (%v, %v), want (nil, false)", it, done)
	}
	if got := item.amount.Load(); got != 0 {
		t.Errorf("amount after exhausted = %d, want 0", got)
	}
}

// TestWeightItemHit_Unlimited 验证 amount < 0 表示无限库存：Hit 永远返回 (w, false) 且不减 amount。
func TestWeightItemHit_Unlimited(t *testing.T) {
	item := &weight[int]{ratio: 10, data: 1}
	item.amount.Store(-1)

	for i := 0; i < 100; i++ {
		it, done := item.Hit()
		if it == nil {
			t.Fatalf("Hit %d: unlimited should always return non-nil", i)
		}
		if done {
			t.Errorf("Hit %d: unlimited should never return done=true", i)
		}
	}
	if got := item.amount.Load(); got != -1 {
		t.Errorf("amount changed: %d, want -1", got)
	}
}

// TestWeightItemHit_ConcurrentExactCount 关键并发正确性测试：
// 起 N 个 goroutine 各自 Hit，amount 设为 M < N。总成功次数必须恰好 M，
// 且最终 amount == 0。
//
// 原实现（非原子 `--`）在这个测试里会出现：
//   - 总成功次数 > M（丢失递减）
//   - amount < 0（被手动 clamp 但已丢 Hit）
//   - 最坏情况：100 并发 Hit amount=50，成功次数可能是 55~70
func TestWeightItemHit_ConcurrentExactCount(t *testing.T) {
	const (
		concurrency = 200
		stock       = 100
	)

	item := &weight[int]{ratio: 10, data: 1}
	item.amount.Store(stock)

	var succeeded atomic.Int32
	var wg sync.WaitGroup
	wg.Add(concurrency)
	// start barrier：所有 goroutine 同时起跑加剧竞争
	start := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			<-start
			if it, _ := item.Hit(); it != nil {
				succeeded.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := succeeded.Load(); got != stock {
		t.Errorf("total successful Hits = %d, want %d (CAS atomicity broken)", got, stock)
	}
	if got := item.amount.Load(); got != 0 {
		t.Errorf("final amount = %d, want 0", got)
	}
}

// TestWeightItemHit_ConcurrentLastHitDone 验证"最后一次 Hit 返回 done=true"的语义
// 在并发下仍然正确：stock 次成功的 Hit 中，恰好有一次 done=true。
func TestWeightItemHit_ConcurrentLastHitDone(t *testing.T) {
	const (
		concurrency = 50
		stock       = 10
	)

	item := &weight[int]{ratio: 10, data: 1}
	item.amount.Store(stock)

	var doneCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(concurrency)
	start := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			<-start
			if _, done := item.Hit(); done {
				doneCount.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := doneCount.Load(); got != 1 {
		t.Errorf("done=true count = %d, want exactly 1", got)
	}
}
