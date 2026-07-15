// tgf v2 示例：metrics + trace 包 — 可观测性接口
//
// 演示 Counter / Gauge / Histogram 三种指标类型 + Span 追踪的用法。
// 使用内存 Provider（不依赖 Prometheus / OpenTelemetry）。
//
// 运行：cd tgf/example/metrics_trace && go run .
package main

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/thkhxm/tgf/v2/metrics"
	"github.com/thkhxm/tgf/v2/trace"
)

func main() {
	fmt.Println("=== tgf v2 示例：metrics + trace 包 ===")
	fmt.Println()

	// ================================================================
	// Part 1: metrics
	// ================================================================
	fmt.Println("====== metrics ======")
	fmt.Println()

	// ----------------------------------------------------------------
	// 1. 设置 Provider（默认是 NoOp，什么都不记录）
	// ----------------------------------------------------------------
	fmt.Println("--- 1. 设置 Provider ---")

	// 生产环境用 Prometheus adapter，这里用内存实现便于演示
	mem := metrics.NewMemoryProvider()
	metrics.SetProvider(mem)
	defer metrics.SetProvider(metrics.NoopProvider()) // 清理

	fmt.Printf("  当前 Provider: %s\n", metrics.GetProvider().Name()) // memory
	fmt.Println()

	// ----------------------------------------------------------------
	// 2. Counter — 单调递增计数器
	// ----------------------------------------------------------------
	fmt.Println("--- 2. Counter ---")

	reqCounter := metrics.NewCounter("http_requests_total", "HTTP 请求总数")
	errCounter := metrics.NewCounter("http_errors_total", "HTTP 错误总数")

	// 模拟 10 个请求，其中 3 个失败
	for i := 0; i < 10; i++ {
		reqCounter.Inc()
		if i%3 == 0 {
			errCounter.Inc()
		}
	}

	fmt.Printf("  请求总数: %.0f\n", mem.CounterValue("http_requests_total")) // 10
	fmt.Printf("  错误总数: %.0f\n", mem.CounterValue("http_errors_total"))   // 4
	fmt.Println()

	// ----------------------------------------------------------------
	// 3. Gauge — 可升可降的瞬时值
	// ----------------------------------------------------------------
	fmt.Println("--- 3. Gauge ---")

	connGauge := metrics.NewGauge("active_connections", "活跃连接数")

	connGauge.Inc()                                                     // +1
	connGauge.Inc()                                                     // +1
	connGauge.Inc()                                                     // +1
	connGauge.Dec()                                                     // -1
	fmt.Printf("  当前连接数: %.0f\n", mem.GaugeValue("active_connections")) // 2

	connGauge.Set(100)                                                       // 直接设值
	fmt.Printf("  Set(100) 后: %.0f\n", mem.GaugeValue("active_connections")) // 100

	connGauge.Add(-30)                                                       // 可以加负数
	fmt.Printf("  Add(-30) 后: %.0f\n", mem.GaugeValue("active_connections")) // 70
	fmt.Println()

	// ----------------------------------------------------------------
	// 4. Histogram — 分布统计（延迟、大小等）
	// ----------------------------------------------------------------
	fmt.Println("--- 4. Histogram ---")

	// buckets 定义分布边界（上界包含）
	latencyHist := metrics.NewHistogram(
		"request_latency_ms",
		"请求延迟（毫秒）",
		[]float64{1, 5, 10, 25, 50, 100, 250, 500}, // 8 个桶
	)

	// 模拟 20 个请求延迟
	for i := 0; i < 20; i++ {
		latency := float64(rand.Intn(100) + 1) // 1~100ms
		latencyHist.Observe(latency)
	}

	samples := mem.HistogramObservations("request_latency_ms")
	fmt.Printf("  采样数: %d\n", len(samples))
	if len(samples) > 0 {
		// 手动算 p50（排序后取中间值）
		fmt.Printf("  首个样本: %.1fms\n", samples[0])
		fmt.Printf("  末个样本: %.1fms\n", samples[len(samples)-1])
	}
	fmt.Println()

	// ----------------------------------------------------------------
	// 5. 同名指标幂等
	// ----------------------------------------------------------------
	fmt.Println("--- 5. 同名指标幂等 ---")

	c1 := metrics.NewCounter("shared_counter", "共享计数器")
	c2 := metrics.NewCounter("shared_counter", "共享计数器")
	c1.Inc()
	c2.Inc()
	fmt.Printf("  两次 NewCounter 同名 → 同一对象: %.0f\n",
		mem.CounterValue("shared_counter")) // 2
	fmt.Println()

	// ----------------------------------------------------------------
	// 6. 框架内建指标
	// ----------------------------------------------------------------
	fmt.Println("--- 6. 框架内建指标（Server.WithMetrics 后自动触发）---")
	fmt.Println(`
  指标名                                    类型       触发点
  ─────────────────────────────────────────────────────────────
  tgf_gate_connections                      Gauge     handleConn Inc/Dec
  tgf_rpc_latency_ms                        Histogram SendRPCMessage defer
  tgf_rpc_calls_total                       Counter   SendRPCMessage defer
  tgf_rpc_calls_fail_total                  Counter   SendRPCMessage err!=nil
  tgf_rpc_policy_reject_circuit_total       Counter   断路器拒绝
  tgf_rpc_policy_reject_ratelimit_total     Counter   限流拒绝
  tgf_rpc_policy_reject_concurrency_total   Counter   超并发拒绝
  `)

	// ================================================================
	// Part 2: trace
	// ================================================================
	fmt.Println("====== trace ======")
	fmt.Println()

	// ----------------------------------------------------------------
	// 7. 创建 Span
	// ----------------------------------------------------------------
	fmt.Println("--- 7. StartSpan ---")

	ctx := context.Background()
	ctx, span := trace.StartSpan(ctx, "handleLogin")
	defer span.End()

	fmt.Printf("  TraceID: %s\n", span.TraceID())

	// 从 ctx 里读回 trace id
	fmt.Printf("  TraceIDFromContext: %s\n", trace.TraceIDFromContext(ctx))

	// 设置属性和错误
	span.SetAttribute("userId", "u001")
	span.SetAttribute("method", "Login")
	// span.SetError(errors.New("password wrong"))  // 标记失败
	span.End() // 结束（重复 End 安全）
	fmt.Println()

	// ----------------------------------------------------------------
	// 8. 嵌套 Span
	// ----------------------------------------------------------------
	fmt.Println("--- 8. 嵌套 Span ---")

	ctx2, parentSpan := trace.StartSpan(context.Background(), "gateway.handleRequest")
	fmt.Printf("  parent TraceID: %s\n", parentSpan.TraceID())

	// 子 span 会复用父 ctx 里的 trace id
	_, childSpan := trace.StartSpan(ctx2, "rpc.callUserService")
	fmt.Printf("  child  TraceID: %s (和 parent 相同)\n", childSpan.TraceID())

	childSpan.End()
	parentSpan.End()
	fmt.Println()

	// ----------------------------------------------------------------
	// 9. 手动注入 trace id
	// ----------------------------------------------------------------
	fmt.Println("--- 9. 手动注入 trace id ---")

	customCtx := trace.WithTraceID(context.Background(), "my-custom-trace-001")
	fmt.Printf("  手动注入后: %s\n", trace.TraceIDFromContext(customCtx))
	fmt.Println()

	// ----------------------------------------------------------------
	// 10. Tracer 切换
	// ----------------------------------------------------------------
	fmt.Println("--- 10. Tracer 管理 ---")
	fmt.Printf("  当前 Tracer: %s\n", trace.GetTracer().Name()) // noop
	fmt.Println(`
  // 生产接入 OpenTelemetry：
  // import tgfotel "your-repo/tgf-otel"
  // trace.SetTracer(tgfotel.NewTracer("myapp"))
  //
  // 然后所有 StartSpan 自动走 OTEL SDK → Jaeger / Tempo
  `)

	fmt.Println("=== metrics + trace 示例结束 ===")
}
