package rpc

// B4: rpc 层的 metrics 埋点 helper 集中放在这里，方便后续 C 档扩展更多指标
// 时不会污染 rpcserver.go / tcp.go 的主流程。
//
// 设计原则：
//   - 所有指标对象延迟初始化（sync.Once）——允许业务在调用 Server.WithMetrics 之后、
//     真正触发 RPC/连接之前切换 Provider。
//   - 热路径函数（observeRPCCall）里避免 fmt.Sprintf / map 分配，直接调 Histogram.Observe
//     和 Counter.Inc。Label 留到 Prometheus adapter 里再按需拆分。
//   - 内部命名约定：tgf_<subsystem>_<measure>_<unit>。subsystem ∈ {gate, rpc, db, …}。

import (
	"sync"
	"time"

	"github.com/thkhxm/tgf/v2/metrics"
)

var (
	rpcMetricsOnce    sync.Once
	rpcLatencyMs      metrics.Histogram
	rpcCallsTotal     metrics.Counter
	rpcCallsFailTotal metrics.Counter
)

// rpcLatencyBuckets 针对游戏服务器的 RPC 延迟画像：
//   - 大多数调用应当在 10ms 以内
//   - 50~100ms 是跨节点/慢调用
//   - 500ms 以上基本是病态，直接落到 +Inf 桶
// 8 个桶足够支撑 p50/p95/p99 的粗粒度报表。
var rpcLatencyBuckets = []float64{1, 5, 10, 25, 50, 100, 250, 500}

func ensureRPCMetrics() {
	rpcMetricsOnce.Do(func() {
		rpcLatencyMs = metrics.NewHistogram(
			"tgf_rpc_latency_ms",
			"RPC 端到端延迟（毫秒），含排队/序列化/网络/反序列化",
			rpcLatencyBuckets,
		)
		rpcCallsTotal = metrics.NewCounter(
			"tgf_rpc_calls_total",
			"SendRPCMessage 累计调用次数",
		)
		rpcCallsFailTotal = metrics.NewCounter(
			"tgf_rpc_calls_fail_total",
			"SendRPCMessage 累计失败次数（含超时）",
		)
	})
}

// observeRPCCall 由 SendRPCMessage 的 defer 调用。start 是进入 SendRPCMessage
// 的时间戳，err 是最终返回给调用者的错误（可能是找不到服务 / 发送失败 / 超时 / 业务异常）。
//
// 为什么不按 moduleName 拆 label：
//   - metrics 接口约定 label 用 "...string" 交错传，在 NoOp Provider 下不产生成本；
//   - 但 B4 为了演示"最小可用"的埋点，先按全局聚合展示——把 label 拆分留给业务方或
//     后续 C 档的 Prometheus adapter 决定。
//   - 如果这里直接传 module/method，内存 Provider 里会给每个 (module,method) 建独立样本桶，
//     单测断言复杂度会上升。
func observeRPCCall(moduleName, methodName string, start time.Time, err error) {
	_ = moduleName
	_ = methodName
	ensureRPCMetrics()
	elapsedMs := float64(time.Since(start).Microseconds()) / 1000.0
	rpcLatencyMs.Observe(elapsedMs)
	rpcCallsTotal.Inc()
	if err != nil {
		rpcCallsFailTotal.Inc()
	}
}

// resetRPCMetricsOnceForTest 只用于单测：允许 TestObserveRPCCall 切换 Provider
// 之后重新绑定指标对象。生产代码不应调用。
func resetRPCMetricsOnceForTest() {
	rpcMetricsOnce = sync.Once{}
	rpcLatencyMs = nil
	rpcCallsTotal = nil
	rpcCallsFailTotal = nil
}

// ---- E3-rpc：网关入站流量埋点 ----
//
// 与 tgf_gate_connections（tcp.go）配套的两个 counter：
//   - tgf_gate_requests_total        网关收到的业务请求总量（handleConn Logic 分支）
//   - tgf_gate_dropped_requests_total 被丢弃的请求总量（reqChan 背压满 + inactive 连接）
//
// 审计背景（P2 背压静默丢弃）：reqChan 满 / 连接 inactive 时请求被丢，客户端
// 永远等不到 ReqId 响应且原先只有 Debug 日志——现在丢弃路径计数 + Warn 日志，
// 过载问题可观测。

var (
	gateReqMetricsOnce sync.Once
	gateRequestsTotal  metrics.Counter
	gateDroppedTotal   metrics.Counter
)

func ensureGateReqMetrics() {
	gateReqMetricsOnce.Do(func() {
		gateRequestsTotal = metrics.NewCounter(
			"tgf_gate_requests_total",
			"网关收到的业务请求总量（Logic 帧）",
		)
		gateDroppedTotal = metrics.NewCounter(
			"tgf_gate_dropped_requests_total",
			"网关丢弃的业务请求总量（reqChan 背压满 / inactive 连接）",
		)
	})
}

// incGateRequest 网关收到一条业务请求（handleConn 的 Logic 分支调用）。
func incGateRequest() {
	ensureGateReqMetrics()
	gateRequestsTotal.Inc()
}

// incGateDropped 网关丢弃一条业务请求（背压满 / inactive 连接，doLogic 与
// handleConn 的丢弃分支调用）。
func incGateDropped() {
	ensureGateReqMetrics()
	gateDroppedTotal.Inc()
}

// resetGateReqMetricsOnceForTest 只用于单测：允许切换 Provider 后重新绑定。
func resetGateReqMetricsOnceForTest() {
	gateReqMetricsOnce = sync.Once{}
	gateRequestsTotal = nil
	gateDroppedTotal = nil
}
