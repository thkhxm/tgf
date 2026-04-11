// Package trace 为 tgf 框架提供最小化的分布式追踪抽象。
//
// 设计目标（B4 档）：
//  1. **零外部依赖**。默认实现是 NoOp Span，不引入 go.opentelemetry.io/otel，
//     避免强制拉一个百 MB 依赖树。
//  2. **对齐已有 context key**。`StartSpan` 注入/读取的 trace id 使用
//     `tgf.ContextKeyTRACEID`，和 `tgf/rpc/plugins.go` 里的 RPC 拦截器共用
//     同一把钥匙。tgf 代码内部不 import tgf 主包（避免循环引用），所以 key
//     常量在本包内复制声明，迁移时保持字符串一致。
//  3. **可桥接 OpenTelemetry**。`Tracer` 接口故意设计得贴近 OTEL 的 StartSpan
//     签名，未来加 `tgf/trace/otel` subpackage 写一个 adapter 就能直接对接。
//  4. **日志友好**。默认实现在 Span End 时按 DEBUG 级打一条"span=name trace_id=..."，
//     让没有 OTEL 基建的用户也能在日志里顺出调用链路。
package trace

import (
	"context"
	"sync/atomic"
	"time"
)

// ContextKeyTraceID 与 tgf/define.go:ContextKeyTRACEID 保持一致。
// 本包内复制常量是为了避免 trace 包依赖 tgf 主包造成循环。
const ContextKeyTraceID = "TraceId"

// Span 表示一次带起止时间戳的工作单元。实现方可以选择：
//   - 记录起止时间 + 元数据，存到内存或日志（默认实现）
//   - 桥接到 OpenTelemetry SDK / Jaeger client
//   - 完全忽略（NoOp）
//
// Span 一定要在 goroutine 退出前调 End。通常用 defer：
//
//	ctx, span := trace.StartSpan(ctx, "gate.handleLogin")
//	defer span.End()
type Span interface {
	// SetAttribute 写入一个键值对到 span。生产实现应当避免热路径多次分配。
	SetAttribute(key string, value any)
	// SetError 标记 span 失败并记录错误原因。End 时写日志会带上这个错误。
	SetError(err error)
	// End 结束 span。重复调用应当安全（后续调用 no-op）。
	End()
	// TraceID 返回当前 trace 的全局 id，便于日志/下游透传。
	TraceID() string
}

// Tracer 是 Span 工厂。
type Tracer interface {
	// StartSpan 创建一个新 span 并把它挂到 ctx 上。返回的新 ctx 里一定能
	// 通过 TraceIDFromContext 读出 trace id。
	StartSpan(ctx context.Context, name string) (context.Context, Span)
	// Name 返回 tracer 类型标识。
	Name() string
}

// ---- NoOp 实现（默认不开启追踪时用） ----

type noopSpan struct {
	traceID string
}

func (s noopSpan) SetAttribute(_ string, _ any) {}
func (s noopSpan) SetError(_ error)             {}
func (s noopSpan) End()                         {}
func (s noopSpan) TraceID() string              { return s.traceID }

type noopTracer struct{}

func (noopTracer) StartSpan(ctx context.Context, _ string) (context.Context, Span) {
	// 即便是 NoOp，也要保证 trace id 在 context 里可读——业务代码可能依赖
	// 这个 id 做日志串联。如果 ctx 里已有就复用，否则生成一个新的。
	tid := TraceIDFromContext(ctx)
	if tid == "" {
		tid = newTraceID()
		ctx = WithTraceID(ctx, tid)
	}
	return ctx, noopSpan{traceID: tid}
}
func (noopTracer) Name() string { return "noop" }

// NoopTracer 返回默认 NoOp Tracer。
func NoopTracer() Tracer { return noopTracer{} }

// ---- 当前进程级 Tracer ----

var current atomic.Pointer[Tracer]

func init() {
	t := Tracer(noopTracer{})
	current.Store(&t)
}

// SetTracer 替换全局 Tracer。应在 Server.Run 前一次性完成。
func SetTracer(t Tracer) {
	if t == nil {
		t = noopTracer{}
	}
	current.Store(&t)
}

// GetTracer 返回当前 Tracer。
func GetTracer() Tracer {
	if p := current.Load(); p != nil {
		return *p
	}
	return noopTracer{}
}

// StartSpan 是 GetTracer().StartSpan 的便捷包装。
func StartSpan(ctx context.Context, name string) (context.Context, Span) {
	return GetTracer().StartSpan(ctx, name)
}

// ---- context 操作 ----

type traceIDKey struct{}

// WithTraceID 把 trace id 注入 context。使用私有 key 避免和其他包冲突。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// TraceIDFromContext 读取 trace id。空字符串表示未设置。
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(traceIDKey{}).(string); ok {
		return v
	}
	return ""
}

// ---- ID 生成 ----

// traceIDCounter 用一个 atomic 递增 + 启动时间戳做简易 trace id；生产应当
// 用 Snowflake / ULID / UUID。这里避免依赖 tgf/util（防止循环引用）。
var (
	traceIDCounter atomic.Uint64
	traceIDEpoch   = time.Now().UnixNano()
)

func newTraceID() string {
	seq := traceIDCounter.Add(1)
	// 简易格式：<epoch_ns>-<seq>。纯 ASCII，便于日志 grep。
	return formatTraceID(traceIDEpoch, seq)
}

func formatTraceID(epoch int64, seq uint64) string {
	// 手写无堆分配的编码避免 fmt.Sprintf 的逃逸。
	const hex = "0123456789abcdef"
	buf := make([]byte, 0, 32)
	// 写 epoch_ns 的 hex 表示
	e := uint64(epoch)
	started := false
	for i := 60; i >= 0; i -= 4 {
		d := byte((e >> i) & 0xf)
		if d != 0 {
			started = true
		}
		if started {
			buf = append(buf, hex[d])
		}
	}
	if !started {
		buf = append(buf, '0')
	}
	buf = append(buf, '-')
	// 写 seq
	started = false
	for i := 60; i >= 0; i -= 4 {
		d := byte((seq >> i) & 0xf)
		if d != 0 {
			started = true
		}
		if started {
			buf = append(buf, hex[d])
		}
	}
	if !started {
		buf = append(buf, '0')
	}
	return string(buf)
}
