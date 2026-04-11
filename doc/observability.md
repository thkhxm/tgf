# tgf v2 可观测性接入指南

> 最后更新：2026-04-11

tgf v2 在 B4 档给框架补齐了三个可观测性抽象：**metrics / trace / log**。本
文档说明如何给业务项目接入实际的监控基建（Prometheus / Grafana / OpenTelemetry /
Jaeger / Loki）。

**核心原则**：tgf 框架本身**零监控依赖**——默认全部是 NoOp 实现。业务方按
自己已有的基建选择 adapter，接入点都是 `Server.WithMetrics(p)` /
`Server.WithTracer(t)` 这类 builder 方法。

## 三个维度快览

| 维度 | 包 | 默认实现 | 推荐接入 |
|------|----|---------|---------|
| 指标（metrics） | `tgf/metrics` | `NoopProvider` | Prometheus / StatsD |
| 追踪（trace） | `tgf/trace` | `NoopTracer` | OpenTelemetry / Jaeger |
| 日志（log） | `tgf/log` | zap + lumberjack stdout + file | Loki / ELK |

---

## metrics 接入

### 1. 理解接口

```go
package metrics

type Counter interface {
    Inc()
    Add(delta float64)
}
type Gauge interface {
    Inc()
    Dec()
    Add(delta float64)
    Set(value float64)
}
type Histogram interface {
    Observe(value float64)
}
type Provider interface {
    Counter(name, help string, labelNames ...string) Counter
    Gauge(name, help string, labelNames ...string) Gauge
    Histogram(name, help string, buckets []float64, labelNames ...string) Histogram
    Name() string
}
```

### 2. 框架内部已有埋点

这些在 v2 自动触发，用户只要接了 Provider 就能看到：

| 指标 | 类型 | 含义 |
|------|------|------|
| `tgf_gate_connections` | Gauge | 网关当前活跃连接数 |
| `tgf_rpc_latency_ms` | Histogram | RPC 端到端延迟（ms） |
| `tgf_rpc_calls_total` | Counter | RPC 累计调用次数 |
| `tgf_rpc_calls_fail_total` | Counter | RPC 累计失败次数 |
| `tgf_rpc_policy_reject_circuit_total` | Counter | 断路器拒绝次数 |
| `tgf_rpc_policy_reject_ratelimit_total` | Counter | 限流拒绝次数 |
| `tgf_rpc_policy_reject_concurrency_total` | Counter | 超并发拒绝次数 |

### 3. 接入 Prometheus（推荐）

业务方写一个独立 subpackage（tgf 不提供，避免拖依赖）：

```go
// your-repo/tgf-prom/provider.go
package tgfprom

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/thkhxm/tgf/metrics"
)

type Provider struct {
    namespace string
    reg       prometheus.Registerer
}

func NewProvider(namespace string) *Provider {
    return &Provider{
        namespace: namespace,
        reg:       prometheus.DefaultRegisterer,
    }
}

func (p *Provider) Counter(name, help string, _ ...string) metrics.Counter {
    c := prometheus.NewCounter(prometheus.CounterOpts{
        Namespace: p.namespace,
        Name:      name,
        Help:      help,
    })
    p.reg.MustRegister(c)
    return &promCounter{c}
}

func (p *Provider) Gauge(name, help string, _ ...string) metrics.Gauge {
    g := prometheus.NewGauge(prometheus.GaugeOpts{
        Namespace: p.namespace,
        Name:      name,
        Help:      help,
    })
    p.reg.MustRegister(g)
    return &promGauge{g}
}

func (p *Provider) Histogram(name, help string, buckets []float64, _ ...string) metrics.Histogram {
    h := prometheus.NewHistogram(prometheus.HistogramOpts{
        Namespace: p.namespace,
        Name:      name,
        Help:      help,
        Buckets:   buckets,
    })
    p.reg.MustRegister(h)
    return &promHistogram{h}
}

func (p *Provider) Name() string { return "prometheus" }

// --- Counter wrapper ---
type promCounter struct{ c prometheus.Counter }
func (p *promCounter) Inc()              { p.c.Inc() }
func (p *promCounter) Add(v float64)     { if v > 0 { p.c.Add(v) } }

// --- Gauge wrapper ---
type promGauge struct{ g prometheus.Gauge }
func (p *promGauge) Inc()          { p.g.Inc() }
func (p *promGauge) Dec()          { p.g.Dec() }
func (p *promGauge) Add(v float64) { p.g.Add(v) }
func (p *promGauge) Set(v float64) { p.g.Set(v) }

// --- Histogram wrapper ---
type promHistogram struct{ h prometheus.Histogram }
func (p *promHistogram) Observe(v float64) { p.h.Observe(v) }
```

然后在业务 `main.go` 里：

```go
import (
    "net/http"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "github.com/thkhxm/tgf/rpc"
    tgfprom "your-repo/tgf-prom"
)

func main() {
    // 启动 /metrics endpoint
    go http.ListenAndServe(":9100", promhttp.Handler())

    rpc.NewRPCServer().
        WithMetrics(tgfprom.NewProvider("myapp")).
        WithService(myService).
        Run()
}
```

### 4. 接入 StatsD / DataDog / 自定义存储

同样的模式——实现 `metrics.Provider` 接口即可。几十行代码。

### 5. 自定义埋点

业务代码可以直接用 `metrics` 包的便捷函数：

```go
import "github.com/thkhxm/tgf/metrics"

var onlineUsers = metrics.NewGauge("online_users", "当前在线用户数")

func OnUserLogin(uid string) {
    onlineUsers.Inc()
}
func OnUserLogout(uid string) {
    onlineUsers.Dec()
}
```

这些 gauge/counter 会自动走当前注入的 Provider。

---

## trace 接入

### 1. 接口

```go
package trace

type Span interface {
    SetAttribute(key string, value any)
    SetError(err error)
    End()
    TraceID() string
}
type Tracer interface {
    StartSpan(ctx context.Context, name string) (context.Context, Span)
    Name() string
}
```

### 2. 默认行为

默认 `NoopTracer` 仍然生成 trace id 并放到 context 里，业务日志可以用
`trace.TraceIDFromContext(ctx)` 读出来做日志串联——即使不接任何追踪基建，
trace id 也是可用的。

### 3. 接入 OpenTelemetry（推荐）

写一个 adapter 把 `go.opentelemetry.io/otel` 的 Tracer 包装成 `tgf/trace.Tracer`：

```go
// your-repo/tgf-otel/tracer.go
package tgfotel

import (
    "context"
    otelsdk "go.opentelemetry.io/otel"
    oteltrace "go.opentelemetry.io/otel/trace"
    "github.com/thkhxm/tgf/trace"
)

type Tracer struct {
    inner oteltrace.Tracer
}

func NewTracer(serviceName string) *Tracer {
    return &Tracer{inner: otelsdk.Tracer(serviceName)}
}

func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
    newCtx, span := t.inner.Start(ctx, name)
    return newCtx, &spanAdapter{span: span}
}

func (t *Tracer) Name() string { return "otel" }

type spanAdapter struct {
    span oteltrace.Span
}
func (s *spanAdapter) SetAttribute(k string, v any) {
    // 按类型分发到 otel attribute helpers
}
func (s *spanAdapter) SetError(err error) { s.span.RecordError(err) }
func (s *spanAdapter) End()                { s.span.End() }
func (s *spanAdapter) TraceID() string     {
    return s.span.SpanContext().TraceID().String()
}
```

### 4. trace id 透传

tgf 已经把 trace id 塞进 rpcx 的 `share.Context` 的 `ContextKeyTRACEID` 字段，
下游服务可以通过 `rpc.GetUserId(ctx)` 类似的 helper 读出来。配合 OpenTelemetry
的 W3C TraceContext 传播器，可以实现完整的分布式链路。

---

## 日志接入（B5）

### 1. 热路径优化

v2 的 `log.*Tag` 系列现在会先做 level + tag 过滤，再决定要不要 `fmt.Sprintf`。
日志级别设置为 WARN 时所有 `Debug*` / `Info*` 调用零分配开销。

### 2. zap.Field 风格 API（推荐新代码）

```go
log.DebugTagW("gate", "连接接入",
    zap.String("addr", addr),
    zap.Int("fd", fd),
    zap.Duration("latency", elapsed),
)
```

相比老 `log.DebugTag("gate", "连接接入 addr=%s fd=%d latency=%v", ...)`：

- 无 `fmt.Sprintf` 分配
- 无 interface{} 装箱
- 日志收集器（Loki / ELK）能按字段结构化索引

### 3. 接入 Loki / ELK

tgf 的 zap core 用 JSON encoder 输出文件日志（`logs/*.log`），可以直接接
Filebeat / Promtail。每条日志包含：

```json
{
  "time": "2026-04-11 20:30:00.000",
  "level": "INFO",
  "caller": "[rpc/tcp.go:451]",
  "message": "接收到一条新的连接",
  "tag": "tcp",
  "addr": "10.0.0.1:52134",
  "fd": 27
}
```

业务只需要在 Loki / ELK 侧配置按 `tag` / `level` / `trace_id` 做 label
索引即可。

---

## 完整接入示例

```go
package main

import (
    "net/http"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "github.com/thkhxm/tgf/config"
    "github.com/thkhxm/tgf/log"
    "github.com/thkhxm/tgf/rpc"
    "github.com/thkhxm/tgf/trace"
    tgfotel "your-repo/tgf-otel"
    tgfprom "your-repo/tgf-prom"
)

func main() {
    // 1. 加载配置
    cfg, err := config.Load()
    if err != nil {
        log.ErrorW("配置加载失败", zap.Error(err))
        return
    }

    // 2. metrics server
    go http.ListenAndServe(":9100", promhttp.Handler())

    // 3. 监听配置热更
    config.OnReload(func(c *config.Config) {
        log.InfoW("配置热更触发", zap.String("level", c.Logger.Level))
    })

    // 4. 启动 tgf server
    rpc.NewRPCServer().
        WithMetrics(tgfprom.NewProvider("myapp")).
        WithTracer(tgfotel.NewTracer("myapp")).
        WithMethodPolicy("gate.Login", rpc.MethodPolicy{
            Timeout:   3 * time.Second,
            RateLimit: 500,
            CircuitBreaker: rpc.CircuitBreakerConfig{
                FailureThreshold: 5,
                RecoveryInterval: 30 * time.Second,
            },
        }).
        WithHealthCheck(10 * time.Second).
        WithGatewayOptions(rpc.GatewayOptions{
            TCPPort: cfg.Service.Port,
            WSPath:  "/ws",
        }).
        WithService(myService).
        Run()
}
```

## 参考

- Prometheus Go client: https://github.com/prometheus/client_golang
- OpenTelemetry Go: https://github.com/open-telemetry/opentelemetry-go
- zap structured logging: https://github.com/uber-go/zap
- Loki: https://grafana.com/oss/loki/
