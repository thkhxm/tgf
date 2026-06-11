package prometheus

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
	tgfmetrics "github.com/thkhxm/tgf/v2/metrics"
)

// gatherInto 从 Provider 的 registry 采集全部指标，按 metric 全名建索引。
func gatherInto(t *testing.T, p *Provider) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := p.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather 失败: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

// TestProvider_SatisfiesInterface 编译期已断言，这里再做一次运行期确认。
func TestProvider_SatisfiesInterface(t *testing.T) {
	var _ tgfmetrics.Provider = NewProvider()
	if NewProvider().Name() != "prometheus" {
		t.Fatalf("Name 期望 prometheus, 实际 %v", NewProvider().Name())
	}
}

// TestProvider_CounterCollect 验证 Counter 注册后能被采集且值正确。
func TestProvider_CounterCollect(t *testing.T) {
	p := NewProvider()
	c := p.Counter("req_total", "请求数")
	c.Inc()
	c.Add(4)

	fams := gatherInto(t, p)
	mf, ok := fams["req_total"]
	if !ok {
		t.Fatalf("未采集到 req_total, 实际: %v", famNames(fams))
	}
	if got := mf.GetMetric()[0].GetCounter().GetValue(); got != 5 {
		t.Fatalf("counter 期望 5, 实际 %v", got)
	}
}

// TestProvider_CounterIgnoresNegative 验证 Counter.Add(<0) 被静默忽略（不 panic、不递减）。
func TestProvider_CounterIgnoresNegative(t *testing.T) {
	p := NewProvider()
	c := p.Counter("safe", "h")
	c.Add(3)
	c.Add(-100) // 必须被忽略，否则 prometheus.Counter.Add(<0) 会 panic
	fams := gatherInto(t, p)
	if got := fams["safe"].GetMetric()[0].GetCounter().GetValue(); got != 3 {
		t.Fatalf("counter 负增长应被忽略, 期望 3, 实际 %v", got)
	}
}

// TestProvider_GaugeCollect 验证 Gauge 的 Inc/Dec/Set/Add。
func TestProvider_GaugeCollect(t *testing.T) {
	p := NewProvider()
	g := p.Gauge("conn", "连接数")
	g.Inc()
	g.Inc()
	g.Dec()
	g.Add(10)
	g.Set(42)
	g.Add(-2)

	fams := gatherInto(t, p)
	if got := fams["conn"].GetMetric()[0].GetGauge().GetValue(); got != 40 {
		t.Fatalf("gauge 期望 40, 实际 %v", got)
	}
}

// TestProvider_HistogramCollect 验证 Histogram 桶计数。
func TestProvider_HistogramCollect(t *testing.T) {
	p := NewProvider()
	h := p.Histogram("lat_ms", "延迟", []float64{10, 50, 100})
	for _, v := range []float64{5, 20, 80, 150} {
		h.Observe(v)
	}
	fams := gatherInto(t, p)
	m := fams["lat_ms"].GetMetric()[0].GetHistogram()
	if m.GetSampleCount() != 4 {
		t.Fatalf("histogram 样本数期望 4, 实际 %v", m.GetSampleCount())
	}
	if m.GetSampleSum() != 255 {
		t.Fatalf("histogram 样本和期望 255, 实际 %v", m.GetSampleSum())
	}
	// 校验桶累计：<=10 有 1（值 5）；<=50 有 2（5,20）；<=100 有 3（5,20,80）
	want := map[float64]uint64{10: 1, 50: 2, 100: 3}
	for _, b := range m.GetBucket() {
		if exp, ok := want[b.GetUpperBound()]; ok && b.GetCumulativeCount() != exp {
			t.Fatalf("桶 le=%v 累计期望 %d, 实际 %d", b.GetUpperBound(), exp, b.GetCumulativeCount())
		}
	}
}

// TestProvider_SameNameIdempotent 验证同名指标复用同一句柄（与内存 Provider 语义一致），
// 不会触发 prometheus 的 AlreadyRegisteredError。
func TestProvider_SameNameIdempotent(t *testing.T) {
	p := NewProvider()
	c1 := p.Counter("dup", "h")
	c2 := p.Counter("dup", "h") // 不应 panic / 不应重复注册
	c1.Inc()
	c2.Inc()
	fams := gatherInto(t, p)
	if got := fams["dup"].GetMetric()[0].GetCounter().GetValue(); got != 2 {
		t.Fatalf("同名 counter 应复用, 期望 2, 实际 %v", got)
	}
}

// TestProvider_Namespace 验证 namespace/subsystem 前缀拼接进最终指标名。
func TestProvider_Namespace(t *testing.T) {
	p := NewProvider(WithNamespace("tgf"), WithSubsystem("gate"))
	p.Counter("conn_total", "h").Inc()
	fams := gatherInto(t, p)
	const want = "tgf_gate_conn_total"
	if _, ok := fams[want]; !ok {
		t.Fatalf("期望指标名 %s, 实际: %v", want, famNames(fams))
	}
}

// TestProvider_Labeled 验证声明 labelNames 的指标可注册可采集，
// 且观测落到全空 label 的固定子指标（tgf 观测接口不带 label 值）。
func TestProvider_Labeled(t *testing.T) {
	p := NewProvider()
	c := p.Counter("labeled_total", "h", "module", "method")
	c.Inc()
	c.Add(2)
	fams := gatherInto(t, p)
	mf, ok := fams["labeled_total"]
	if !ok {
		t.Fatalf("未采集到 labeled_total, 实际: %v", famNames(fams))
	}
	m := mf.GetMetric()[0]
	if got := m.GetCounter().GetValue(); got != 3 {
		t.Fatalf("labeled counter 期望 3, 实际 %v", got)
	}
	// 两个 label 都应在场（值为空串）。
	if len(m.GetLabel()) != 2 {
		t.Fatalf("期望 2 个 label, 实际 %d", len(m.GetLabel()))
	}
	for _, l := range m.GetLabel() {
		if l.GetValue() != "" {
			t.Fatalf("label %s 期望空串, 实际 %q", l.GetName(), l.GetValue())
		}
	}
}

// TestProvider_AsTGFProvider 端到端验证：把 Provider 注入 tgf metrics 全局，
// 用顶层便捷函数 NewCounter 埋点，能被本 registry 采集到——证明"接线真生效"。
func TestProvider_AsTGFProvider(t *testing.T) {
	defer tgfmetrics.SetProvider(tgfmetrics.NoopProvider())

	p := NewProvider(WithNamespace("tgf"))
	tgfmetrics.SetProvider(p)

	// 模拟框架内建埋点的调用方式（metrics_hooks.go / tcp.go 即如此）。
	tgfmetrics.NewCounter("rpc_calls_total", "RPC 调用次数").Inc()
	tgfmetrics.NewGauge("gate_connections", "连接数").Set(7)
	tgfmetrics.NewHistogram("rpc_latency_ms", "延迟", []float64{1, 5, 10}).Observe(3)

	fams := gatherInto(t, p)
	if v := fams["tgf_rpc_calls_total"].GetMetric()[0].GetCounter().GetValue(); v != 1 {
		t.Fatalf("rpc_calls_total 期望 1, 实际 %v", v)
	}
	if v := fams["tgf_gate_connections"].GetMetric()[0].GetGauge().GetValue(); v != 7 {
		t.Fatalf("gate_connections 期望 7, 实际 %v", v)
	}
	if c := fams["tgf_rpc_latency_ms"].GetMetric()[0].GetHistogram().GetSampleCount(); c != 1 {
		t.Fatalf("rpc_latency_ms 样本数期望 1, 实际 %v", c)
	}
}

func famNames(m map[string]*dto.MetricFamily) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
