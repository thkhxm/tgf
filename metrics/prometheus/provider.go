// Package prometheus 是 tgf metrics.Provider 接口的官方 Prometheus 适配器（E3 档）。
//
// 背景：tgf/metrics 顶层包只提供接口 + NoOp + 内存测试实现，刻意不引入
// prometheus/client_golang，以免强制不需要监控的使用者背上这棵依赖树。
// 本子包是"显式选择"的产物——业务 main 里 import 它，把 tgf 内建埋点
// （tgf_gate_connections / tgf_rpc_latency_ms / tgf_rpc_calls_total 等）
// 真正接到 prometheus 文本端点，补齐审计指出的"从指标到可抓取端点的最后一公里"。
//
// 典型用法：
//
//	p := prometheus.NewProvider(prometheus.WithNamespace("tgf"))
//	metrics.SetProvider(p)                 // 必须在 Server.Run() 之前
//	go prometheus.ServeMetrics(p, ":9100") // 暴露 /metrics 端点
//
// 设计取舍：
//   - 每个 Provider 持有独立 *prometheus.Registry（不写全局 DefaultRegisterer），
//     多 Server / 多测试场景互不串扰，也规避审计指出的全局单例覆盖问题。
//   - 同名指标幂等：重复调用 Counter(name,...) 返回同一句柄，不会触发
//     prometheus 的 AlreadyRegisteredError（这与内存 Provider 的语义一致）。
//   - Label 处理：tgf metrics.Counter/Gauge/Histogram 的 Inc/Add/Observe 接口
//     在观测时不携带 label 值（见 tgf/metrics 包注释——label 维度是声明式的）。
//     因此声明了 labelNames 的指标，本适配器注册为 *Vec 并把返回句柄绑定到
//     "全部 label 取空串"的固定子指标；未声明 label 的指标直接映射为基础类型。
//
//nolint:revive // 包名 prometheus 与第三方同名是有意为之（子包语义明确）。
package prometheus

import (
	"sync"

	prom "github.com/prometheus/client_golang/prometheus"
	tgfmetrics "github.com/thkhxm/tgf/metrics"
)

// Option 配置 Provider 的可选项。
type Option func(*options)

type options struct {
	namespace string
	subsystem string
}

// WithNamespace 给所有指标名加 prometheus namespace 前缀（最终名形如 <ns>_<name>）。
func WithNamespace(ns string) Option {
	return func(o *options) { o.namespace = ns }
}

// WithSubsystem 给所有指标名加 prometheus subsystem 中段（最终名形如 <ns>_<sub>_<name>）。
func WithSubsystem(sub string) Option {
	return func(o *options) { o.subsystem = sub }
}

// Provider 是 metrics.Provider 的 Prometheus 实现。
type Provider struct {
	opts     options
	registry *prom.Registry

	mu         sync.Mutex
	counters   map[string]tgfmetrics.Counter
	gauges     map[string]tgfmetrics.Gauge
	histograms map[string]tgfmetrics.Histogram
}

// 编译期断言：Provider 必须满足 tgf metrics.Provider 契约。
var _ tgfmetrics.Provider = (*Provider)(nil)

// NewProvider 构造一个 Prometheus Provider，持有自己的 Registry。
func NewProvider(opts ...Option) *Provider {
	p := &Provider{
		registry:   prom.NewRegistry(),
		counters:   map[string]tgfmetrics.Counter{},
		gauges:     map[string]tgfmetrics.Gauge{},
		histograms: map[string]tgfmetrics.Histogram{},
	}
	for _, o := range opts {
		o(&p.opts)
	}
	return p
}

// Registry 暴露底层 prometheus.Registry，供 ServeMetrics / 自定义 collector 注册使用。
func (p *Provider) Registry() *prom.Registry { return p.registry }

// Name 返回 provider 类型标识。
func (p *Provider) Name() string { return "prometheus" }

// emptyValues 为声明了 labelNames 的指标生成"全部取空串"的 label 值切片。
// tgf 的观测接口不携带 label 值，故所有内建埋点都落到这一个固定子指标上；
// 业务若需要 label 维度拆分，应直接用本包暴露的 Registry 注册自定义 *Vec。
func emptyValues(labelNames []string) []string {
	if len(labelNames) == 0 {
		return nil
	}
	vs := make([]string, len(labelNames))
	return vs // 默认零值即空串
}

// Counter 实现 metrics.Provider.Counter。
func (p *Provider) Counter(name, help string, labelNames ...string) tgfmetrics.Counter {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.counters[name]; ok {
		return c
	}
	opts := prom.CounterOpts{Namespace: p.opts.namespace, Subsystem: p.opts.subsystem, Name: name, Help: help}
	var c tgfmetrics.Counter
	if len(labelNames) == 0 {
		pc := prom.NewCounter(opts)
		p.registry.MustRegister(pc)
		c = promCounter{c: pc}
	} else {
		vec := prom.NewCounterVec(opts, labelNames)
		p.registry.MustRegister(vec)
		// 绑定到全空 label 的子指标，使 Inc/Add 无需携带 label 值即可工作。
		c = promCounter{c: vec.WithLabelValues(emptyValues(labelNames)...)}
	}
	p.counters[name] = c
	return c
}

// Gauge 实现 metrics.Provider.Gauge。
func (p *Provider) Gauge(name, help string, labelNames ...string) tgfmetrics.Gauge {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g, ok := p.gauges[name]; ok {
		return g
	}
	opts := prom.GaugeOpts{Namespace: p.opts.namespace, Subsystem: p.opts.subsystem, Name: name, Help: help}
	var g tgfmetrics.Gauge
	if len(labelNames) == 0 {
		pg := prom.NewGauge(opts)
		p.registry.MustRegister(pg)
		g = promGauge{g: pg}
	} else {
		vec := prom.NewGaugeVec(opts, labelNames)
		p.registry.MustRegister(vec)
		g = promGauge{g: vec.WithLabelValues(emptyValues(labelNames)...)}
	}
	p.gauges[name] = g
	return g
}

// Histogram 实现 metrics.Provider.Histogram。
func (p *Provider) Histogram(name, help string, buckets []float64, labelNames ...string) tgfmetrics.Histogram {
	p.mu.Lock()
	defer p.mu.Unlock()
	if h, ok := p.histograms[name]; ok {
		return h
	}
	opts := prom.HistogramOpts{
		Namespace: p.opts.namespace,
		Subsystem: p.opts.subsystem,
		Name:      name,
		Help:      help,
		Buckets:   buckets,
	}
	var h tgfmetrics.Histogram
	if len(labelNames) == 0 {
		ph := prom.NewHistogram(opts)
		p.registry.MustRegister(ph)
		h = promHistogram{h: ph}
	} else {
		vec := prom.NewHistogramVec(opts, labelNames)
		p.registry.MustRegister(vec)
		// HistogramVec.WithLabelValues 返回 prometheus.Observer。
		h = promHistogram{h: vec.WithLabelValues(emptyValues(labelNames)...)}
	}
	p.histograms[name] = h
	return h
}

// ---- 三个观测句柄包装：把 prometheus 类型适配到 tgf metrics 接口 ----

// promCounter 把 prometheus.Counter 适配到 tgf metrics.Counter。
type promCounter struct{ c prom.Counter }

func (p promCounter) Inc() { p.c.Inc() }

// Add 遵循 tgf Counter 契约：负增长被静默忽略（prometheus.Counter.Add(<0) 会 panic）。
func (p promCounter) Add(delta float64) {
	if delta < 0 {
		return
	}
	p.c.Add(delta)
}

// promGauge 把 prometheus.Gauge 适配到 tgf metrics.Gauge。
type promGauge struct{ g prom.Gauge }

func (p promGauge) Inc()              { p.g.Inc() }
func (p promGauge) Dec()              { p.g.Dec() }
func (p promGauge) Add(delta float64) { p.g.Add(delta) }
func (p promGauge) Set(v float64)     { p.g.Set(v) }

// promHistogram 把 prometheus.Observer 适配到 tgf metrics.Histogram。
type promHistogram struct{ h prom.Observer }

func (p promHistogram) Observe(v float64) { p.h.Observe(v) }
