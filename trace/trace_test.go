package trace

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestNoopTracer_InjectsTraceIDOnEmptyContext(t *testing.T) {
	ctx, span := NoopTracer().StartSpan(context.Background(), "unit")
	defer span.End()

	if span.TraceID() == "" {
		t.Fatal("NoOp Tracer 应当生成 trace id")
	}
	if got := TraceIDFromContext(ctx); got != span.TraceID() {
		t.Fatalf("ctx 里的 trace id 应等于 span.TraceID, got %q vs %q", got, span.TraceID())
	}
}

func TestNoopTracer_PreservesExistingTraceID(t *testing.T) {
	ctx := WithTraceID(context.Background(), "pre-existing")
	_, span := NoopTracer().StartSpan(ctx, "unit")
	defer span.End()
	if span.TraceID() != "pre-existing" {
		t.Fatalf("应复用 ctx 里已有的 trace id, got %q", span.TraceID())
	}
}

func TestSpan_SetAttributeAndEndAreSafe(t *testing.T) {
	_, span := NoopTracer().StartSpan(context.Background(), "unit")
	span.SetAttribute("k", "v")
	span.SetError(errors.New("boom"))
	span.End()
	span.End() // 重复调用应当安全
}

func TestSetTracer_SwitchesCurrent(t *testing.T) {
	defer SetTracer(NoopTracer())

	rec := &recordingTracer{}
	SetTracer(rec)

	ctx, span := StartSpan(context.Background(), "topfn")
	span.End()

	if rec.calls != 1 {
		t.Fatalf("期望 recordingTracer 被调用 1 次, 实际 %d", rec.calls)
	}
	if TraceIDFromContext(ctx) != "fake-id" {
		t.Fatalf("ctx 里应当带有 recordingTracer 注入的 trace id, got %q", TraceIDFromContext(ctx))
	}
}

func TestSetTracer_NilFallsBackToNoop(t *testing.T) {
	defer SetTracer(NoopTracer())
	SetTracer(nil)
	if GetTracer().Name() != "noop" {
		t.Fatalf("nil 应 fallback 到 noop, 实际 %v", GetTracer().Name())
	}
}

func TestTraceIDFromContext_NilSafe(t *testing.T) {
	if got := TraceIDFromContext(nil); got != "" {
		t.Fatalf("nil ctx 应返回空, 实际 %q", got)
	}
	if got := TraceIDFromContext(context.Background()); got != "" {
		t.Fatalf("未设置应返回空, 实际 %q", got)
	}
}

func TestNewTraceID_Unique(t *testing.T) {
	const N = 1000
	seen := make(map[string]struct{}, N)
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			id := newTraceID()
			mu.Lock()
			seen[id] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != N {
		t.Fatalf("期望 %d 个不同 trace id, 实际 %d", N, len(seen))
	}
}

// --- 测试辅助 ---

type recordingTracer struct {
	calls int
}

func (r *recordingTracer) StartSpan(ctx context.Context, _ string) (context.Context, Span) {
	r.calls++
	ctx = WithTraceID(ctx, "fake-id")
	return ctx, noopSpan{traceID: "fake-id"}
}
func (r *recordingTracer) Name() string { return "recording" }
