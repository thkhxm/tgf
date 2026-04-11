package metrics

import (
	"sync"
	"testing"
)

// TestNoopProvider_DoesNothing 验证 NoOp Provider 的空实现不会 panic，
// 同时不记录任何值。
func TestNoopProvider_DoesNothing(t *testing.T) {
	p := NoopProvider()
	c := p.Counter("x", "help")
	g := p.Gauge("y", "help")
	h := p.Histogram("z", "help", []float64{1, 2, 3})

	c.Inc()
	c.Add(10)
	g.Inc()
	g.Dec()
	g.Add(5)
	g.Set(100)
	h.Observe(1.5)

	if p.Name() != "noop" {
		t.Fatalf("期望 noop, 实际 %v", p.Name())
	}
}

// TestMemoryProvider_CounterAddsMonotonically 验证内存 Counter 单调递增。
func TestMemoryProvider_CounterAddsMonotonically(t *testing.T) {
	m := NewMemoryProvider()
	c := m.Counter("req", "请求数")
	c.Inc()
	c.Inc()
	c.Add(3)
	if v := m.CounterValue("req"); v != 5 {
		t.Fatalf("期望 5, 实际 %v", v)
	}
	// 负数应被静默忽略
	c.Add(-10)
	if v := m.CounterValue("req"); v != 5 {
		t.Fatalf("Counter 不应接受负数, 实际 %v", v)
	}
}

// TestMemoryProvider_GaugeIncDecSet 验证 Gauge 的双向操作。
func TestMemoryProvider_GaugeIncDecSet(t *testing.T) {
	m := NewMemoryProvider()
	g := m.Gauge("conn", "连接数")
	g.Inc()
	g.Inc()
	g.Inc()
	g.Dec()
	if v := m.GaugeValue("conn"); v != 2 {
		t.Fatalf("期望 2, 实际 %v", v)
	}
	g.Set(100)
	if v := m.GaugeValue("conn"); v != 100 {
		t.Fatalf("Set 后期望 100, 实际 %v", v)
	}
	g.Add(-50)
	if v := m.GaugeValue("conn"); v != 50 {
		t.Fatalf("Add(-50) 后期望 50, 实际 %v", v)
	}
}

// TestMemoryProvider_HistogramCollectsSamples 验证 Histogram 快照。
func TestMemoryProvider_HistogramCollectsSamples(t *testing.T) {
	m := NewMemoryProvider()
	h := m.Histogram("lat", "延迟", []float64{10, 50, 100})
	for _, v := range []float64{5, 20, 80, 150} {
		h.Observe(v)
	}
	samples := m.HistogramObservations("lat")
	if len(samples) != 4 {
		t.Fatalf("期望 4 个样本, 实际 %v", len(samples))
	}
}

// TestMemoryProvider_SameNameReturnsSameInstance 验证同名指标幂等。
// 业务热路径常在每个请求里调 NewCounter(name)，实现必须保证只创建一次。
func TestMemoryProvider_SameNameReturnsSameInstance(t *testing.T) {
	m := NewMemoryProvider()
	c1 := m.Counter("req", "请求数")
	c2 := m.Counter("req", "请求数")
	c1.Inc()
	c2.Inc()
	if v := m.CounterValue("req"); v != 2 {
		t.Fatalf("同名 counter 应复用, 实际值 %v", v)
	}
}

// TestSetProvider_SwitchesCurrent 验证全局 Provider 切换。
func TestSetProvider_SwitchesCurrent(t *testing.T) {
	// 保证测试隔离
	defer SetProvider(NoopProvider())

	m := NewMemoryProvider()
	SetProvider(m)

	c := NewCounter("top", "顶层便捷函数")
	c.Inc()

	if v := m.CounterValue("top"); v != 1 {
		t.Fatalf("NewCounter 应路由到当前 Provider, 实际 %v", v)
	}
	if GetProvider().Name() != "memory" {
		t.Fatalf("GetProvider 应返回 memory, 实际 %v", GetProvider().Name())
	}
}

// TestSetProvider_NilFallsBackToNoop 验证传 nil 的保护路径。
func TestSetProvider_NilFallsBackToNoop(t *testing.T) {
	defer SetProvider(NoopProvider())
	SetProvider(nil)
	if GetProvider().Name() != "noop" {
		t.Fatalf("nil 应 fallback 到 noop, 实际 %v", GetProvider().Name())
	}
}

// TestMemoryProvider_ConcurrentCounter 验证 counter 在并发场景的正确性。
func TestMemoryProvider_ConcurrentCounter(t *testing.T) {
	m := NewMemoryProvider()
	c := m.Counter("hot", "并发计数")

	const (
		goroutines = 50
		perRoutine = 1000
	)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perRoutine; j++ {
				c.Inc()
			}
		}()
	}
	wg.Wait()

	expected := float64(goroutines * perRoutine)
	if v := m.CounterValue("hot"); v != expected {
		t.Fatalf("期望 %v, 实际 %v", expected, v)
	}
}
