package trace

// E3 桥接测试：trace 包与 rpcx ReqMetaData 的鸭子类型贯通。
//
// 这里用 fakeMetaCtx 模拟 rpcx share.Context 的方法集（GetReqMetaDataByKey /
// SetReqMetaData）；与真实 *share.Context 的端到端贯通测试在 tgf/rpc 包
// （trace_e2e_test.go，经真实 rpcx 协议透传）。

import (
	"context"
	"testing"
)

// fakeMetaCtx 模拟 rpcx share.Context：实现 metaReader / metaWriter 方法集。
type fakeMetaCtx struct {
	context.Context
	meta map[string]string
}

func newFakeMetaCtx(meta map[string]string) *fakeMetaCtx {
	if meta == nil {
		meta = map[string]string{}
	}
	return &fakeMetaCtx{Context: context.Background(), meta: meta}
}

func (f *fakeMetaCtx) GetReqMetaDataByKey(key string) string { return f.meta[key] }
func (f *fakeMetaCtx) SetReqMetaData(key, val string)        { f.meta[key] = val }

func TestTraceIDFromContext_ReadsMetaCarrier(t *testing.T) {
	ctx := newFakeMetaCtx(map[string]string{ContextKeyTraceID: "gw-trace-1"})
	if got := TraceIDFromContext(ctx); got != "gw-trace-1" {
		t.Fatalf("应从 ReqMetaData 桥接读取 trace id, got %q", got)
	}
}

func TestTraceIDFromContext_PrivateKeyPriority(t *testing.T) {
	// 私有 key 包装在 meta carrier 之上时，私有 key（更局部）优先。
	inner := newFakeMetaCtx(map[string]string{ContextKeyTraceID: "meta-id"})
	ctx := context.WithValue(inner, traceIDKey{}, "local-id")
	if got := TraceIDFromContext(ctx); got != "local-id" {
		t.Fatalf("私有 key 应优先于 meta, got %q", got)
	}
}

func TestWithTraceID_MetaCarrierInPlace(t *testing.T) {
	ctx := newFakeMetaCtx(nil)
	out := WithTraceID(ctx, "tid-42")
	// 关键断言：不允许包装 meta carrier（会破坏 *share.Context 类型断言），
	// 必须原地写 meta 并返回原 ctx。
	if out != context.Context(ctx) {
		t.Fatalf("meta carrier 应原地写入并返回原 ctx, got %T", out)
	}
	if ctx.meta[ContextKeyTraceID] != "tid-42" {
		t.Fatalf("ReqMetaData 应被写入 trace id, meta=%v", ctx.meta)
	}
	if got := TraceIDFromContext(out); got != "tid-42" {
		t.Fatalf("写入后应可读回, got %q", got)
	}
}

func TestStartSpan_ReusesGatewayMetaTraceID(t *testing.T) {
	// 模拟"网关生成 trace id → 后端 service handler 内 StartSpan"：
	// span 必须复用 meta 里的链路 id，而不是另起炉灶生成新 id。
	ctx := newFakeMetaCtx(map[string]string{ContextKeyTraceID: "gw-snowflake-99"})
	outCtx, span := StartSpan(ctx, "svc.handler")
	defer span.End()

	if span.TraceID() != "gw-snowflake-99" {
		t.Fatalf("StartSpan 应复用网关链路 trace id, got %q", span.TraceID())
	}
	if got := TraceIDFromContext(outCtx); got != "gw-snowflake-99" {
		t.Fatalf("返回 ctx 的 trace id 应一致, got %q", got)
	}
}

func TestStartSpan_EmptyMetaCarrierInjects(t *testing.T) {
	// meta carrier 没有 trace id 时：StartSpan 生成新 id 并写回 ReqMetaData
	// （经 WithTraceID 桥接），后续 RPC 透传同一 id。
	ctx := newFakeMetaCtx(nil)
	_, span := StartSpan(ctx, "svc.first")
	defer span.End()

	if span.TraceID() == "" {
		t.Fatal("应生成新 trace id")
	}
	if got := ctx.meta[ContextKeyTraceID]; got != span.TraceID() {
		t.Fatalf("新 id 应写回 ReqMetaData 供下游透传, meta=%q span=%q", got, span.TraceID())
	}
}
