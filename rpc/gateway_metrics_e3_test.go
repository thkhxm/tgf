package rpc

// E3-rpc 埋点接线测试（MemoryProvider 断言）：
//   1. tgf_gate_requests_total —— handleConn 的 Logic 分支真实计数；
//   2. tgf_gate_dropped_requests_total —— doLogic 对 inactive 连接的丢弃计数；
//   3. 网关主链路 sendMessage 接入 RPC 时延/调用量/错误率指标
//      （tgf_rpc_latency_ms / tgf_rpc_calls_total / tgf_rpc_calls_fail_total）。

import (
	"sync"
	"testing"
	"time"

	"github.com/thkhxm/tgf/v2/metrics"
)

// memMetricsReader 是 MemoryProvider 断言面（NewMemoryProvider 返回非导出类型，
// 用接口投影其断言方法）。
type memMetricsReader interface {
	CounterValue(string) float64
	GaugeValue(string) float64
	HistogramObservations(string) []float64
}

// withMemoryMetrics 切换到 MemoryProvider 并重置所有延迟初始化的指标绑定，
// 返回断言句柄；Cleanup 恢复 NoOp。
func withMemoryMetrics(t *testing.T) memMetricsReader {
	t.Helper()
	m := metrics.NewMemoryProvider()
	metrics.SetProvider(m)
	resetRPCMetricsOnceForTest()
	resetGateReqMetricsOnceForTest()
	resetGateConnGaugeOnceForTest()
	policyCounterMap = sync.Map{}
	t.Cleanup(func() {
		metrics.SetProvider(metrics.NoopProvider())
		resetRPCMetricsOnceForTest()
		resetGateReqMetricsOnceForTest()
		resetGateConnGaugeOnceForTest()
		policyCounterMap = sync.Map{}
	})
	return m
}

// TestGateRequestCounter_WiredIntoHandleConn 驱动真实 handleConn：
// mockConn 注入一条 Logic 帧，tgf_gate_requests_total 必须 +1（接线证明，
// 不是只测 helper 函数）。
func TestGateRequestCounter_WiredIntoHandleConn(t *testing.T) {
	p := withMemoryMetrics(t)
	_, cleanup := withLocalEchoDispatch(t, "echo.Echo")
	defer cleanup()

	srv := newTestServer()
	mock := newMockConn(false)
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleConn(mock)
	}()

	// 注入一条 Logic 帧（echo.Echo 在白名单内，能走完 doLogic 全链路）。
	mock.frames <- &FrameIn{
		MessageType: Logic,
		Module:      "echo",
		Method:      "Echo",
		Data:        newEchoFrameData(t, []byte("metrics")),
	}

	waitUntil(t, 2*time.Second, func() bool {
		return p.CounterValue("tgf_gate_requests_total") >= 1
	})

	_ = mock.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn 未退出")
	}
}

// TestGateDroppedCounter_InactiveConnection 验证 doLogic 对 inactive 连接的
// 丢弃路径计入 tgf_gate_dropped_requests_total。
func TestGateDroppedCounter_InactiveConnection(t *testing.T) {
	p := withMemoryMetrics(t)

	srv := newTestServer()
	u := newBareConnectData()
	u.Offline(false) // 置为 Offlining → isActiveForLogic() == false

	srv.doLogic(&RequestData{
		User:          u,
		Module:        "echo",
		RequestMethod: "Echo",
		Data:          nil,
	})

	if got := p.CounterValue("tgf_gate_dropped_requests_total"); got != 1 {
		t.Fatalf("tgf_gate_dropped_requests_total = %v, want 1", got)
	}
}

// TestSendMessage_ObservesRPCMetrics 验证网关主链路 sendMessage 接入了
// RPC 时延/调用量/错误率指标（E3：原先只有 SendRPCMessage 有埋点）。
func TestSendMessage_ObservesRPCMetrics(t *testing.T) {
	p := withMemoryMetrics(t)
	_, cleanup := withLocalEchoDispatch(t, "echo.Echo")
	defer cleanup()

	ct := newBareConnectData()
	args := &Args[*WSMessage]{ByteData: newEchoFrameData(t, []byte("m"))}

	// 成功一次
	if err := sendMessage(ct, "echo", "Echo", args, &Reply[*WSMessage]{}); err != nil {
		t.Fatalf("sendMessage err=%v", err)
	}
	// 失败一次（模块不存在 + rpcClient 不可用）
	orig := loadRPCClient()
	storeRPCClient(nil)
	_ = sendMessage(ct, "ghost", "Nope", args, &Reply[*WSMessage]{})
	storeRPCClient(orig)

	if got := p.CounterValue("tgf_rpc_calls_total"); got != 2 {
		t.Errorf("tgf_rpc_calls_total = %v, want 2", got)
	}
	if got := p.CounterValue("tgf_rpc_calls_fail_total"); got != 1 {
		t.Errorf("tgf_rpc_calls_fail_total = %v, want 1", got)
	}
	if obs := p.HistogramObservations("tgf_rpc_latency_ms"); len(obs) != 2 {
		t.Errorf("tgf_rpc_latency_ms 观测数 = %d, want 2", len(obs))
	}
}
