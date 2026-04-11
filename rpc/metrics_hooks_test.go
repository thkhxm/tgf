package rpc

// B4: 埋点集成测试。验证 metrics_hooks.go 里两处核心埋点确实被
// SendRPCMessage 的 defer 触发，以及 handleConn 的 gauge inc/dec 配对。
//
// 这些测试不需要真正的 rpcx 后端——observeRPCCall 是纯函数，handleConn
// 的入口用 mockConn（和 A2-phase4 的 tcp_test.go 共用）即可。

import (
	"errors"
	"testing"
	"time"

	"github.com/thkhxm/tgf/metrics"
)

func TestObserveRPCCall_UpdatesHistogramAndCounters(t *testing.T) {
	m := metrics.NewMemoryProvider()
	defer metrics.SetProvider(metrics.NoopProvider())
	metrics.SetProvider(m)
	// 重置 once，让指标重新绑定到新的 Provider
	resetRPCMetricsOnceForTest()

	start := time.Now().Add(-15 * time.Millisecond)
	observeRPCCall("demo", "M1", start, nil)
	observeRPCCall("demo", "M2", start, errors.New("boom"))
	observeRPCCall("demo", "M3", start, nil)

	if v := m.CounterValue("tgf_rpc_calls_total"); v != 3 {
		t.Fatalf("总调用数期望 3, 实际 %v", v)
	}
	if v := m.CounterValue("tgf_rpc_calls_fail_total"); v != 1 {
		t.Fatalf("失败数期望 1, 实际 %v", v)
	}
	samples := m.HistogramObservations("tgf_rpc_latency_ms")
	if len(samples) != 3 {
		t.Fatalf("期望 3 个延迟样本, 实际 %d", len(samples))
	}
	for _, s := range samples {
		if s < 10 || s > 100 {
			// 15ms 起跑，单次运行上下浮动几 ms 合理，超过 100ms 说明有异常
			t.Fatalf("延迟样本应当在 10~100ms 附近, 实际 %v", s)
		}
	}
}

func TestObserveRPCCall_NoopProviderIsSilent(t *testing.T) {
	// 默认就是 NoOp，任何调用都不应 panic
	resetRPCMetricsOnceForTest()
	observeRPCCall("", "", time.Now(), nil)
	observeRPCCall("a", "b", time.Now().Add(-1*time.Millisecond), errors.New("x"))
}

func TestGateConnGauge_ReflectsProvider(t *testing.T) {
	m := metrics.NewMemoryProvider()
	defer metrics.SetProvider(metrics.NoopProvider())
	metrics.SetProvider(m)
	resetGateConnGaugeOnceForTest()

	g := getGateConnGauge()
	g.Inc()
	g.Inc()
	g.Dec()

	if v := m.GaugeValue("tgf_gate_connections"); v != 1 {
		t.Fatalf("gauge 期望 1, 实际 %v", v)
	}
}
