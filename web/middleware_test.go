package web

// G1 单测：内置中间件——Recover / Trace / AccessLog / Metrics / RateLimit / Auth
// 与 Backend context 注入。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thkhxm/tgf/v2/metrics"
	"github.com/thkhxm/tgf/v2/trace"
)

// TestChain_Order 验证 Chain 的包装顺序：mws[0] 最外层；nil 中间件被跳过。
func TestChain_Order(t *testing.T) {
	rec := &orderRecorder{}
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rec.add("handler")
	}), mkOrderMW(rec, "outer"), nil, mkOrderMW(rec, "inner"))

	doReq(t, h, http.MethodGet, "/", nil)
	got := rec.snapshot()
	want := []string{"outer", "inner", "handler"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("Chain 执行顺序 = %v, want %v", got, want)
	}
}

// TestRecover_PanicReturns500 验证 handler panic 被恢复并返回 500，服务可继续处理后续请求。
func TestRecover_PanicReturns500(t *testing.T) {
	calls := 0
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			panic("boom")
		}
		_, _ = w.Write([]byte("alive"))
	}), Recover())

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp1, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("第一次请求失败: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusInternalServerError {
		t.Errorf("panic 请求状态码 = %d, want 500", resp1.StatusCode)
	}

	resp2, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("panic 后服务应继续可用: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("panic 后第二次请求状态码 = %d, want 200", resp2.StatusCode)
	}
}

// TestRecover_AbortHandlerPassesThrough 验证 http.ErrAbortHandler 按标准库约定原样上抛。
func TestRecover_AbortHandlerPassesThrough(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}), Recover())

	defer func() {
		if p := recover(); p != http.ErrAbortHandler {
			t.Errorf("ErrAbortHandler 应原样上抛, 实际 recover = %v", p)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

// TestTrace_GenerateAndPropagate 验证 traceId 的生成与透传：
//   - 无入站头 → 生成新 id，注入 ctx 且回写响应头；
//   - 有入站 X-Trace-Id → 复用同一 id（HTTP 链路与上游一致）。
func TestTrace_GenerateAndPropagate(t *testing.T) {
	var captured string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = trace.TraceIDFromContext(r.Context())
		_, _ = w.Write([]byte("ok"))
	}), Trace())

	// 无入站头：生成新 id
	_, _, header := doReq(t, h, http.MethodGet, "/", nil)
	respTID := header.Get(HeaderTraceID)
	if respTID == "" {
		t.Fatal("无入站头时应生成 traceId 并回写响应头")
	}
	if captured == "" || captured != respTID {
		t.Errorf("ctx 内 traceId = %q, 响应头 = %q, 应一致且非空", captured, respTID)
	}

	// 有入站头：原样复用
	_, _, header = doReq(t, h, http.MethodGet, "/", map[string]string{HeaderTraceID: "up-trace-1"})
	if captured != "up-trace-1" {
		t.Errorf("入站 traceId 应透传进 ctx, got %q, want up-trace-1", captured)
	}
	if got := header.Get(HeaderTraceID); got != "up-trace-1" {
		t.Errorf("响应头 traceId = %q, want up-trace-1", got)
	}
}

// TestAccessLog_EmitsStructuredLine 验证访问日志包含 method/path/status/traceId。
func TestAccessLog_EmitsStructuredLine(t *testing.T) {
	var (
		mu    sync.Mutex
		lines []string
	)
	oldFn := accessLogFn
	accessLogFn = func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	t.Cleanup(func() { accessLogFn = oldFn })

	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	}), Trace(), AccessLog())

	doReq(t, h, http.MethodGet, "/teapot", nil)

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 {
		t.Fatalf("访问日志条数 = %d, want 1", len(lines))
	}
	line := lines[0]
	for _, want := range []string{"method=GET", "path=/teapot", "status=418", "bytes=15", "traceId="} {
		if !strings.Contains(line, want) {
			t.Errorf("访问日志缺少 %q: %s", want, line)
		}
	}
	if strings.Contains(line, "traceId= ") || strings.Contains(line, "traceId= ") {
		t.Errorf("访问日志 traceId 不应为空: %s", line)
	}
}

// swapMemoryProvider 切换到内存 metrics Provider 并重绑 HTTP 指标，cleanup 还原。
func swapMemoryProvider(t *testing.T) *metricsMemProvider {
	t.Helper()
	mem := metrics.NewMemoryProvider()
	metrics.SetProvider(mem)
	resetHTTPMetricsForTest()
	t.Cleanup(func() {
		metrics.SetProvider(nil) // 还原 NoOp
		resetHTTPMetricsForTest()
	})
	return &metricsMemProvider{mem: mem}
}

// metricsMemProvider 是内存 Provider 的断言包装（NewMemoryProvider 返回私有类型，
// 这里通过接口断言读数）。
type metricsMemProvider struct {
	mem interface {
		CounterValue(name string) float64
		GaugeValue(name string) float64
		HistogramObservations(name string) []float64
	}
}

// TestMetrics_CountsAndLatency 验证 Metrics 中间件：请求量、状态码分类、
// 时延直方图、in-flight 归零；panic→500（Recover 在内层）同样计数。
func TestMetrics_CountsAndLatency(t *testing.T) {
	p := swapMemoryProvider(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/bad", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "bad", http.StatusBadRequest) })
	mux.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	h := Chain(mux, Metrics(), Recover())

	doReq(t, h, http.MethodGet, "/ok", nil)
	doReq(t, h, http.MethodGet, "/ok", nil)
	doReq(t, h, http.MethodGet, "/bad", nil)
	doReq(t, h, http.MethodGet, "/panic", nil)

	if got := p.mem.CounterValue("tgf_http_requests_total"); got != 4 {
		t.Errorf("tgf_http_requests_total = %v, want 4", got)
	}
	if got := p.mem.CounterValue("tgf_http_responses_4xx_total"); got != 1 {
		t.Errorf("tgf_http_responses_4xx_total = %v, want 1", got)
	}
	if got := p.mem.CounterValue("tgf_http_responses_5xx_total"); got != 1 {
		t.Errorf("tgf_http_responses_5xx_total = %v, want 1 (panic→500 也应计数)", got)
	}
	if got := len(p.mem.HistogramObservations("tgf_http_request_duration_ms")); got != 4 {
		t.Errorf("时延直方图样本数 = %v, want 4", got)
	}
	if got := p.mem.GaugeValue("tgf_http_inflight_requests"); got != 0 {
		t.Errorf("请求完成后 in-flight = %v, want 0", got)
	}
}

// TestRateLimit_Returns429 验证限流：超额请求返回 429 并计入指标；
// 令牌恢复后重新放行（E1 同款令牌桶语义）。
func TestRateLimit_Returns429(t *testing.T) {
	p := swapMemoryProvider(t)

	h := Chain(http.HandlerFunc(okHandlerFunc), RateLimit(NewTokenBucketLimiter(1)))
	srv := httptest.NewServer(h)
	defer srv.Close()

	get := func() int {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if st := get(); st != http.StatusOK {
		t.Errorf("桶满时首个请求 = %d, want 200", st)
	}
	if st := get(); st != http.StatusTooManyRequests {
		t.Errorf("超额请求 = %d, want 429", st)
	}
	if got := p.mem.CounterValue("tgf_http_ratelimited_total"); got != 1 {
		t.Errorf("tgf_http_ratelimited_total = %v, want 1", got)
	}

	// 1 QPS：等待令牌恢复后应重新放行
	deadline := time.Now().Add(3 * time.Second)
	for {
		if get() == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("令牌恢复后应重新放行")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestRateLimit_NilLimiterPassthrough 验证 nil limiter 透传。
func TestRateLimit_NilLimiterPassthrough(t *testing.T) {
	h := Chain(http.HandlerFunc(okHandlerFunc), RateLimit(nil))
	if st, _, _ := doReq(t, h, http.MethodGet, "/", nil); st != http.StatusOK {
		t.Errorf("nil limiter 应透传, 状态码 = %d", st)
	}
}

// TestTokenBucketLimiter_ZeroQPSAllowsAll 验证 qps<=0 退化为不限流。
func TestTokenBucketLimiter_ZeroQPSAllowsAll(t *testing.T) {
	l := NewTokenBucketLimiter(0)
	for i := 0; i < 100; i++ {
		if !l.Allow() {
			t.Fatal("qps<=0 应恒放行")
		}
	}
}

// TestAuth_FailClosedAndConstantTimeToken 验证鉴权中间件（D6 同款语义）：
//   - check 为 nil / 口令未配置 → 503（fail-closed）；
//   - 无口令 / 错口令 → 401；
//   - Bearer / 裸 Authorization / X-Api-Token 三种携带方式均可通过。
func TestAuth_FailClosedAndConstantTimeToken(t *testing.T) {
	cases := []struct {
		name   string
		mw     Middleware
		header map[string]string
		want   int
	}{
		{"nil check fail-closed", Auth(nil), nil, http.StatusServiceUnavailable},
		{"空口令 fail-closed", Auth(StaticBearerToken("")), map[string]string{"Authorization": "Bearer x"}, http.StatusServiceUnavailable},
		{"无凭据", Auth(StaticBearerToken("sec")), nil, http.StatusUnauthorized},
		{"错口令", Auth(StaticBearerToken("sec")), map[string]string{"Authorization": "Bearer wrong"}, http.StatusUnauthorized},
		{"Bearer 正确", Auth(StaticBearerToken("sec")), map[string]string{"Authorization": "Bearer sec"}, http.StatusOK},
		{"裸 Authorization", Auth(StaticBearerToken("sec")), map[string]string{"Authorization": "sec"}, http.StatusOK},
		{"X-Api-Token", Auth(StaticBearerToken("sec")), map[string]string{HeaderAPIToken: "sec"}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := Chain(http.HandlerFunc(okHandlerFunc), tc.mw)
			if st, _, _ := doReq(t, h, http.MethodGet, "/", tc.header); st != tc.want {
				t.Errorf("状态码 = %d, want %d", st, tc.want)
			}
		})
	}
}

// TestAuth_TokenProviderHotSwap 验证 BearerToken 的 provider 每次请求取值
// （支持热轮换：换口令后旧口令立即失效）。
func TestAuth_TokenProviderHotSwap(t *testing.T) {
	var (
		mu    sync.Mutex
		token = "v1"
	)
	provider := func() string {
		mu.Lock()
		defer mu.Unlock()
		return token
	}
	h := Chain(http.HandlerFunc(okHandlerFunc), Auth(BearerToken(provider)))

	if st, _, _ := doReq(t, h, http.MethodGet, "/", map[string]string{"Authorization": "Bearer v1"}); st != http.StatusOK {
		t.Fatalf("轮换前正确口令应通过, 状态码 = %d", st)
	}
	mu.Lock()
	token = "v2"
	mu.Unlock()
	if st, _, _ := doReq(t, h, http.MethodGet, "/", map[string]string{"Authorization": "Bearer v1"}); st != http.StatusUnauthorized {
		t.Errorf("轮换后旧口令应失效, 状态码 = %d", st)
	}
	if st, _, _ := doReq(t, h, http.MethodGet, "/", map[string]string{"Authorization": "Bearer v2"}); st != http.StatusOK {
		t.Errorf("轮换后新口令应通过, 状态码 = %d", st)
	}
}

// TestBackend_ContextInjection 验证 WithBackend / BackendFromContext /
// BackendFromRequest 的注入与读取。
func TestBackend_ContextInjection(t *testing.T) {
	echo := BackendFunc(func(_ context.Context, module, method string, args, reply any) error {
		in, ok1 := args.(*string)
		out, ok2 := reply.(*string)
		if !ok1 || !ok2 {
			return errors.New("类型不符")
		}
		*out = module + "." + method + ":" + *in
		return nil
	})

	// context 直接注入/读取
	ctx := WithBackend(context.Background(), echo)
	b, ok := BackendFromContext(ctx)
	if !ok {
		t.Fatal("BackendFromContext 应命中")
	}
	args, reply := "hi", ""
	if err := b.Invoke(ctx, "m", "F", &args, &reply); err != nil || reply != "m.F:hi" {
		t.Errorf("Invoke = (%v, %q), want (nil, m.F:hi)", err, reply)
	}

	// 经中间件注入 + BackendFromRequest 读取
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rb, rok := BackendFromRequest(r)
		if !rok {
			http.Error(w, "no backend", http.StatusInternalServerError)
			return
		}
		a, rep := "req", ""
		if err := rb.Invoke(r.Context(), "mod", "Go", &a, &rep); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(rep))
	}), backendMiddleware(echo))

	if st, body, _ := doReq(t, h, http.MethodGet, "/", nil); st != http.StatusOK || body != "mod.Go:req" {
		t.Errorf("经中间件注入的 Backend 调用 = (%d, %q), want (200, mod.Go:req)", st, body)
	}

	// 未注入时读取应失败
	if _, ok := BackendFromContext(context.Background()); ok {
		t.Error("未注入时 BackendFromContext 应返回 false")
	}
}
