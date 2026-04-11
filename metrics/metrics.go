// Package metrics 为 tgf 框架提供一组最小化的可观测性指标接口。
//
// 设计目标（B4 档）：
//  1. **零外部依赖**。默认实现是 NoOp，不引入 prometheus/client_golang，
//     让使用者在不需要基建时也能放心引用 tgf。
//  2. **接口 vs 实现分离**。`Counter` / `Gauge` / `Histogram` / `Provider` 四个接口
//     是框架的公共契约；真正的 Prometheus / OpenTelemetry 适配器应放到
//     独立 subpackage（如 `tgf/metrics/prom`），由业务 repo 显式选择是否引入。
//  3. **低分配热路径**。Inc / Add / Observe 这些调用会出现在每帧处理里，
//     NoOp 实现内联为空方法；约定实现方需要尽量避免每次调用产生堆分配。
//  4. **Label 字符串约定**。键值对统一用 `...string` 的交错参数（`"k1","v1","k2","v2"`），
//     避免 `map[string]string` 的分配；超过 6 对 label 的场景建议拆指标名。
//
// 典型用法：
//
//	// 业务 main 函数里按需选择 provider：
//	tgf.Server.WithMetrics(metrics.NoopProvider())           // 默认
//	tgf.Server.WithMetrics(prom.NewProvider(prom.WithNamespace("tgf")))
//
//	// tgf 内部埋点：
//	var (
//	    gateConn  = metrics.Gauge("tgf_gate_connections", "网关当前活跃连接数")
//	    rpcLatency = metrics.Histogram("tgf_rpc_latency_ms", "RPC 端到端延迟", []float64{1, 5, 10, 50, 100, 500})
//	)
//	gateConn.Inc()
//	defer gateConn.Dec()
//
// 注意：`metrics.Counter/Gauge/Histogram` 这些顶层函数是对 *当前* Provider 的便捷封装。
// Provider 通过 `SetProvider` 注入，默认是 `NoopProvider()`，SetProvider 之前创建的
// 指标引用仍然指向旧 Provider（不会自动迁移），因此 Provider 切换应当在 `Run()`
// 之前一次性完成。
package metrics

import (
	"sync"
	"sync/atomic"
)

// Counter 单调递增计数器。适合统计事件次数：请求数、错误数、消息吞吐。
type Counter interface {
	// Inc 等价于 Add(1)。热路径上调 Inc 比调 Add(1) 少一次 box。
	Inc()
	// Add 允许负增长？—— 不允许。Counter 语义上必须 >= 0，Add 传负数实现应忽略或 panic（NoOp 静默忽略）。
	Add(delta float64)
}

// Gauge 可升可降的瞬时值。适合在线连接数、队列长度、内存占用。
type Gauge interface {
	Inc()
	Dec()
	Add(delta float64)
	Set(value float64)
}

// Histogram 分布统计。适合 RPC 延迟、payload 大小。
// buckets 语义与 Prometheus 对齐——上界包含，必须单调递增。
type Histogram interface {
	Observe(value float64)
}

// Provider 是指标工厂。实现方负责把 name+labels 映射到底层存储
// （Prometheus CollectorVec、OpenTelemetry Meter、内存 map 等等）。
//
// 调用约定：
//   - `name` 必须稳定字符串常量，避免动态拼接（Prometheus 会按 name 建立常驻 collector）。
//   - `help` 是一句话描述，Prometheus 会序列化到 /metrics 端点注释。
//   - `labelNames` 声明允许的 label key 全集；各指标 Inc/Add 时按顺序提供 value。
//     Provider 实现可以选择忽略 labelNames（NoOp 就忽略），但必须能安全接受任意顺序的 kv 对。
type Provider interface {
	Counter(name, help string, labelNames ...string) Counter
	Gauge(name, help string, labelNames ...string) Gauge
	Histogram(name, help string, buckets []float64, labelNames ...string) Histogram
	// Name 返回 provider 类型标识，便于日志/诊断。
	Name() string
}

// ---- NoOp 实现（默认） ----

type noopCounter struct{}

func (noopCounter) Inc()          {}
func (noopCounter) Add(_ float64) {}

type noopGauge struct{}

func (noopGauge) Inc()          {}
func (noopGauge) Dec()          {}
func (noopGauge) Add(_ float64) {}
func (noopGauge) Set(_ float64) {}

type noopHistogram struct{}

func (noopHistogram) Observe(_ float64) {}

type noopProvider struct{}

func (noopProvider) Counter(_, _ string, _ ...string) Counter {
	return noopCounter{}
}
func (noopProvider) Gauge(_, _ string, _ ...string) Gauge {
	return noopGauge{}
}
func (noopProvider) Histogram(_, _ string, _ []float64, _ ...string) Histogram {
	return noopHistogram{}
}
func (noopProvider) Name() string { return "noop" }

// NoopProvider 返回一个什么都不做的 Provider。tgf 默认使用它，保证
// 不强制使用者引入任何监控依赖。
func NoopProvider() Provider { return noopProvider{} }

// ---- 当前进程级 Provider ----

var current atomic.Pointer[Provider]

func init() {
	p := Provider(noopProvider{})
	current.Store(&p)
}

// SetProvider 全局替换当前 Provider。应当在 Server.Run 之前完成一次性设置。
// 切换后已创建的指标引用不会自动迁移（见 package doc）。
func SetProvider(p Provider) {
	if p == nil {
		p = noopProvider{}
	}
	current.Store(&p)
}

// GetProvider 返回当前 Provider。
func GetProvider() Provider {
	if p := current.Load(); p != nil {
		return *p
	}
	return noopProvider{}
}

// ---- 便捷函数（顶层 API） ----

// NewCounter / NewGauge / NewHistogram 是对 GetProvider().XXX 的直通。
// 它们存在的目的只是让埋点点位的代码更短：`metrics.NewCounter(...)` vs
// `metrics.GetProvider().Counter(...)`。
func NewCounter(name, help string, labelNames ...string) Counter {
	return GetProvider().Counter(name, help, labelNames...)
}

func NewGauge(name, help string, labelNames ...string) Gauge {
	return GetProvider().Gauge(name, help, labelNames...)
}

func NewHistogram(name, help string, buckets []float64, labelNames ...string) Histogram {
	return GetProvider().Histogram(name, help, buckets, labelNames...)
}

// ---- 内置的内存 Provider（仅供测试和本地诊断使用） ----

// memoryProvider 是一份线程安全的内存实现，用来在单测里断言埋点是否触发。
// 它**不**适合生产——没有导出端点、没有标签拆分、没有衰减。
type memoryProvider struct {
	mu         sync.Mutex
	counters   map[string]*memCounter
	gauges     map[string]*memGauge
	histograms map[string]*memHistogram
}

// NewMemoryProvider 返回一个内存 Provider，暴露 Snapshot() 便于断言。
func NewMemoryProvider() *memoryProvider {
	return &memoryProvider{
		counters:   map[string]*memCounter{},
		gauges:     map[string]*memGauge{},
		histograms: map[string]*memHistogram{},
	}
}

func (m *memoryProvider) Counter(name, _ string, _ ...string) Counter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.counters[name]; ok {
		return c
	}
	c := &memCounter{}
	m.counters[name] = c
	return c
}

func (m *memoryProvider) Gauge(name, _ string, _ ...string) Gauge {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.gauges[name]; ok {
		return g
	}
	g := &memGauge{}
	m.gauges[name] = g
	return g
}

func (m *memoryProvider) Histogram(name, _ string, _ []float64, _ ...string) Histogram {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.histograms[name]; ok {
		return h
	}
	h := &memHistogram{}
	m.histograms[name] = h
	return h
}

func (m *memoryProvider) Name() string { return "memory" }

// CounterValue 返回指定 counter 的当前值，找不到返回 0。
func (m *memoryProvider) CounterValue(name string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.counters[name]; ok {
		return c.Value()
	}
	return 0
}

// GaugeValue 返回指定 gauge 的当前值。
func (m *memoryProvider) GaugeValue(name string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.gauges[name]; ok {
		return g.Value()
	}
	return 0
}

// HistogramObservations 返回指定 histogram 的全部观测值（按观测顺序）。
func (m *memoryProvider) HistogramObservations(name string) []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.histograms[name]; ok {
		return h.Snapshot()
	}
	return nil
}

type memCounter struct {
	mu  sync.Mutex
	val float64
}

func (c *memCounter) Inc() {
	c.mu.Lock()
	c.val++
	c.mu.Unlock()
}

func (c *memCounter) Add(delta float64) {
	if delta < 0 {
		return
	}
	c.mu.Lock()
	c.val += delta
	c.mu.Unlock()
}

func (c *memCounter) Value() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.val
}

type memGauge struct {
	mu  sync.Mutex
	val float64
}

func (g *memGauge) Inc()              { g.Add(1) }
func (g *memGauge) Dec()              { g.Add(-1) }
func (g *memGauge) Set(v float64)     { g.mu.Lock(); g.val = v; g.mu.Unlock() }
func (g *memGauge) Add(delta float64) { g.mu.Lock(); g.val += delta; g.mu.Unlock() }
func (g *memGauge) Value() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.val
}

type memHistogram struct {
	mu      sync.Mutex
	samples []float64
}

func (h *memHistogram) Observe(v float64) {
	h.mu.Lock()
	h.samples = append(h.samples, v)
	h.mu.Unlock()
}

func (h *memHistogram) Snapshot() []float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]float64, len(h.samples))
	copy(out, h.samples)
	return out
}
