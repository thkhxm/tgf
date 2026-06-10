package web

import (
	"crypto/subtle"
	"errors"
	"math"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/metrics"
	"github.com/thkhxm/tgf/trace"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G1：HTTP 内置中间件——
//  Trace      生成/透传 traceId（与 rpcx ReqMetaData 同一套 trace 包，HTTP→RPC 全链路同 id）
//  AccessLog  结构化访问日志（带 traceId / 状态码 / 耗时）
//  Metrics    请求量 / 时延 / 状态码 / in-flight（复用 E3 metrics.Provider）
//  Recover    handler panic → 500（不杀进程）
//  RateLimit  限流（E1 同款令牌桶语义，可注入自定义 Limiter）
//  Auth       鉴权（参考 D6：constant-time 比对 + fail-closed）
//2026/6/10
//***************************************************

// ---- 响应记录器（状态码 / 字节数捕获，供日志与 metrics 共用）----

// ResponseRecorder 包装 http.ResponseWriter，捕获最终状态码与写出字节数。
// Server 的内置中间件链共享同一个实例（ensureRecorder 幂等包装）。
type ResponseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

// WriteHeader 记录第一次写入的状态码（与 net/http 只生效一次的语义对齐）。
func (rw *ResponseRecorder) WriteHeader(code int) {
	if rw.status == 0 {
		rw.status = code
	}
	rw.ResponseWriter.WriteHeader(code)
}

// Write 统计写出字节数；未显式 WriteHeader 时按 net/http 语义视为 200。
func (rw *ResponseRecorder) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += int64(n)
	return n, err
}

// Status 返回响应状态码；handler 没写任何东西时按 net/http 语义返回 200。
func (rw *ResponseRecorder) Status() int {
	if rw.status == 0 {
		return http.StatusOK
	}
	return rw.status
}

// BytesWritten 返回已写出的 body 字节数。
func (rw *ResponseRecorder) BytesWritten() int64 { return rw.bytes }

// Wrote 返回 handler 是否已经开始写响应（决定 Recover 还能不能回 500）。
func (rw *ResponseRecorder) Wrote() bool { return rw.status != 0 }

// Unwrap 支持 http.ResponseController（Flush / SetWriteDeadline 等穿透到底层）。
func (rw *ResponseRecorder) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

// ensureRecorder 幂等包装：w 已是 *ResponseRecorder 时直接复用（同一请求的
// 多个内置中间件共享一份状态），否则包一层。返回 (记录器, 应传给下一跳的 w)。
func ensureRecorder(w http.ResponseWriter) (*ResponseRecorder, http.ResponseWriter) {
	if rec, ok := w.(*ResponseRecorder); ok {
		return rec, w
	}
	rec := &ResponseRecorder{ResponseWriter: w}
	return rec, rec
}

// ---- Trace ----

// HeaderTraceID 是 HTTP 链路透传 traceId 的请求/响应头。
// 入站请求带该头时直接复用（网关/前置代理生成的链路 id），否则生成新 id；
// 响应总是回写该头，便于客户端/排障关联日志。
const HeaderTraceID = "X-Trace-Id"

// Trace 生成/透传 traceId 并注入请求 context：
//   - 入站 X-Trace-Id 头非空 → 复用；否则经 trace.StartSpan 生成新 id；
//   - 注入后 handler 内 trace.TraceIDFromContext(r.Context()) 即可读到；
//   - rpc 侧注入的 Backend（rpc.WithHTTPService）会把该 id 写进 rpcx
//     ReqMetaData["TraceId"]——HTTP 请求 → 后端 RPC 全链路同一个 traceId。
func Trace() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if tid := strings.TrimSpace(r.Header.Get(HeaderTraceID)); tid != "" {
				ctx = trace.WithTraceID(ctx, tid)
			}
			ctx, span := trace.StartSpan(ctx, r.Method+" "+r.URL.Path)
			defer span.End()
			w.Header().Set(HeaderTraceID, span.TraceID())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---- AccessLog ----

// accessLogFn 是访问日志的输出点（var 而非直调是为了单测注入断言日志内容）。
var accessLogFn = func(format string, args ...any) {
	log.InfoTag("web", format, args...)
}

// AccessLog 输出结构化访问日志：method / path / status / bytes / 耗时 / traceId / remote。
// 放在 Trace 之后（内层）保证日志里 traceId 非空。
func AccessLog() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec, w2 := ensureRecorder(w)
			start := time.Now()
			next.ServeHTTP(w2, r)
			costMs := float64(time.Since(start).Microseconds()) / 1000.0
			accessLogFn("method=%s path=%s status=%d bytes=%d costMs=%.2f traceId=%s remote=%s",
				r.Method, r.URL.Path, rec.Status(), rec.BytesWritten(), costMs,
				trace.TraceIDFromContext(r.Context()), r.RemoteAddr)
		})
	}
}

// ---- Metrics（复用 E3 metrics.Provider，命名约定 tgf_<subsystem>_<measure>）----

var (
	httpMetricsOnce       sync.Once
	httpRequestsTotal     metrics.Counter
	httpResponses4xxTotal metrics.Counter
	httpResponses5xxTotal metrics.Counter
	httpRateLimitedTotal  metrics.Counter
	httpInflightRequests  metrics.Gauge
	httpLatencyMs         metrics.Histogram
)

// httpLatencyBuckets 沿用 rpc 层（metrics_hooks.go）的延迟画像，HTTP 侧多一个 1s 桶
// （web 请求允许比 RPC 略慢的长尾）。
var httpLatencyBuckets = []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000}

// ensureHTTPMetrics 延迟初始化（与 rpc/metrics_hooks.go 同模式）：允许业务在
// metrics.SetProvider 之后、首个请求之前完成 Provider 切换。
func ensureHTTPMetrics() {
	httpMetricsOnce.Do(func() {
		httpRequestsTotal = metrics.NewCounter(
			"tgf_http_requests_total", "HTTP 请求总量")
		httpResponses4xxTotal = metrics.NewCounter(
			"tgf_http_responses_4xx_total", "HTTP 4xx 响应总量")
		httpResponses5xxTotal = metrics.NewCounter(
			"tgf_http_responses_5xx_total", "HTTP 5xx 响应总量")
		httpRateLimitedTotal = metrics.NewCounter(
			"tgf_http_ratelimited_total", "HTTP 被限流拒绝（429）的请求总量")
		httpInflightRequests = metrics.NewGauge(
			"tgf_http_inflight_requests", "HTTP 正在处理中的请求数")
		httpLatencyMs = metrics.NewHistogram(
			"tgf_http_request_duration_ms", "HTTP 请求处理耗时（毫秒）", httpLatencyBuckets)
	})
}

// resetHTTPMetricsForTest 仅供单测：切换 Provider 后重新绑定指标对象。
func resetHTTPMetricsForTest() {
	httpMetricsOnce = sync.Once{}
	httpRequestsTotal = nil
	httpResponses4xxTotal = nil
	httpResponses5xxTotal = nil
	httpRateLimitedTotal = nil
	httpInflightRequests = nil
	httpLatencyMs = nil
}

// Metrics 输出请求量 / 时延 / 状态码分类 / in-flight 指标。
// defer 观测保证 handler panic（由内层 Recover 转 500）时计数依然准确。
func Metrics() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ensureHTTPMetrics()
			rec, w2 := ensureRecorder(w)
			httpInflightRequests.Inc()
			start := time.Now()
			defer func() {
				httpInflightRequests.Dec()
				httpLatencyMs.Observe(float64(time.Since(start).Microseconds()) / 1000.0)
				httpRequestsTotal.Inc()
				switch st := rec.Status(); {
				case st >= 500:
					httpResponses5xxTotal.Inc()
				case st >= 400:
					httpResponses4xxTotal.Inc()
				}
			}()
			next.ServeHTTP(w2, r)
		})
	}
}

// ---- Recover ----

// Recover 捕获 handler panic：记结构化错误日志（带 traceId 与堆栈）并在响应
// 尚未写出时返回 500——单个请求 panic 绝不能杀死整个 HTTP 服务。
// http.ErrAbortHandler 按 net/http 约定原样上抛（这是标准库的"主动断开"信号）。
func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec, w2 := ensureRecorder(w)
			defer func() {
				if p := recover(); p != nil {
					if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(p)
					}
					if p == http.ErrAbortHandler {
						panic(p)
					}
					log.ErrorTag("web", "HTTP handler panic(已恢复) method=%s path=%s traceId=%s panic=%v\n%s",
						r.Method, r.URL.Path, trace.TraceIDFromContext(r.Context()), p, debug.Stack())
					if !rec.Wrote() {
						http.Error(w2, "internal server error", http.StatusInternalServerError)
					}
				}
			}()
			next.ServeHTTP(w2, r)
		})
	}
}

// ---- RateLimit（E1 同款令牌桶语义，可注入）----

// Limiter 是限流器的最小注入接口。框架内置 NewTokenBucketLimiter（与 E1
// rpc 策略管道的令牌桶同语义）；业务也可注入自己的实现（如分布式限流）。
type Limiter interface {
	// Allow 返回本次请求是否放行。实现必须并发安全。
	Allow() bool
}

// RateLimit 全局/分组限流：被拒请求返回 429 并计入 tgf_http_ratelimited_total。
// l 为 nil 时透传（容忍未配置）。
func RateLimit(l Limiter) Middleware {
	return func(next http.Handler) http.Handler {
		if l == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow() {
				ensureHTTPMetrics()
				httpRateLimitedTotal.Inc()
				http.Error(w, "rate limited", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// tokenBucketLimiter 与 rpc/rpcserver_policy.go 的 tokenBucket 同语义：
// 互斥锁保护的经典令牌桶，桶容量 = max(1, qps)，启动时桶满。
// （web 包不 import rpc，按 E1 实现复刻以保持两侧限流行为一致。）
type tokenBucketLimiter struct {
	mu       sync.Mutex
	rate     float64
	capacity float64
	tokens   float64
	last     time.Time
}

// NewTokenBucketLimiter 构造一个 qps 令牌桶限流器（E1 同款语义）。
// qps <= 0 返回恒放行的限流器（等价于不限流）。
func NewTokenBucketLimiter(qps float64) Limiter {
	if qps <= 0 {
		return allowAllLimiter{}
	}
	return &tokenBucketLimiter{
		rate:     qps,
		capacity: math.Max(1, qps),
		tokens:   math.Max(1, qps),
		last:     time.Now(),
	}
}

func (t *tokenBucketLimiter) Allow() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(t.last).Seconds()
	t.last = now
	t.tokens += elapsed * t.rate
	if t.tokens > t.capacity {
		t.tokens = t.capacity
	}
	if t.tokens >= 1 {
		t.tokens -= 1
		return true
	}
	return false
}

// allowAllLimiter 恒放行（qps<=0 时的退化实现）。
type allowAllLimiter struct{}

func (allowAllLimiter) Allow() bool { return true }

// ---- Auth（参考 D6 admin token：constant-time + fail-closed）----

// ErrAuthNotConfigured 表示鉴权凭据未配置。Auth 中间件收到它返回 503——
// 与 D6 一致：未配置口令 = 拒绝所有请求（fail-closed），且与 401（带错口令）
// 区分开便于运维定位"是没配置还是配错了"。
var ErrAuthNotConfigured = errors.New("tgf/web: auth credential not configured")

// ErrUnauthorized 表示请求未携带或携带了错误的凭据，Auth 中间件映射为 401。
var ErrUnauthorized = errors.New("tgf/web: unauthorized")

// HeaderAPIToken 是 Bearer 方案之外的备用口令头（便于 curl / 运维工具直传）。
const HeaderAPIToken = "X-Api-Token"

// AuthFunc 是可插拔的鉴权函数：返回 nil 放行；
// 返回 ErrAuthNotConfigured → 503（fail-closed）；其余非 nil → 401。
type AuthFunc func(r *http.Request) error

// Auth 鉴权中间件。check 为 nil 时同样 fail-closed（503）——
// 显式挂了鉴权却没给校验函数，绝不能静默放行。
func Auth(check AuthFunc) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if check == nil {
				log.ErrorTag("web", "鉴权未配置,拒绝请求(fail-closed) path=%s remote=%s", r.URL.Path, r.RemoteAddr)
				http.Error(w, "auth not configured", http.StatusServiceUnavailable)
				return
			}
			if err := check(r); err != nil {
				if errors.Is(err, ErrAuthNotConfigured) {
					log.ErrorTag("web", "鉴权凭据未配置,拒绝请求(fail-closed) path=%s remote=%s", r.URL.Path, r.RemoteAddr)
					http.Error(w, "auth not configured", http.StatusServiceUnavailable)
					return
				}
				log.WarnTag("web", "鉴权失败 path=%s remote=%s err=%v", r.URL.Path, r.RemoteAddr, err)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BearerToken 构造一个 Bearer 口令校验函数（D6 同款语义）：
//   - tokenProvider 每次请求时取期望口令——支持热轮换（如从 tgfconfig.Current() 读）；
//   - 期望口令为空 → ErrAuthNotConfigured（fail-closed）；
//   - 比对走 subtle.ConstantTimeCompare 防时序侧信道；
//   - 请求口令取自 Authorization: Bearer <token>（兼容裸 token）或 X-Api-Token 头。
func BearerToken(tokenProvider func() string) AuthFunc {
	return func(r *http.Request) error {
		if tokenProvider == nil {
			return ErrAuthNotConfigured
		}
		want := strings.TrimSpace(tokenProvider())
		if want == "" {
			return ErrAuthNotConfigured
		}
		got := extractBearerToken(r)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			return ErrUnauthorized
		}
		return nil
	}
}

// StaticBearerToken 是 BearerToken 的固定口令便捷入口。
func StaticBearerToken(token string) AuthFunc {
	return BearerToken(func() string { return token })
}

// extractBearerToken 从请求头提取口令：优先 Authorization: Bearer <token>
// （兼容裸 token 直接放 Authorization 头），回退 X-Api-Token。
func extractBearerToken(r *http.Request) string {
	const bearerPrefix = "Bearer "
	if v := r.Header.Get("Authorization"); v != "" {
		if strings.HasPrefix(v, bearerPrefix) {
			return strings.TrimSpace(v[len(bearerPrefix):])
		}
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(r.Header.Get(HeaderAPIToken))
}
