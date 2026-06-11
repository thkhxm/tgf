package prometheus

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/thkhxm/tgf/v2/log"
)

// MetricsTokenEnv 是 /metrics 端点鉴权口令的环境变量名。
// 与 admin 控制面的 ADMIN_TOKEN 解耦：metrics 抓取通常由 Prometheus server
// 单独配置 bearer_token，不应复用运维口令。默认不开启鉴权（内网抓取场景），
// 配置了本变量或通过 WithToken 显式传入后才强制校验。
const MetricsTokenEnv = "METRICS_TOKEN"

// metricsAuthHeader 是携带 metrics 口令的 HTTP 请求头（Authorization: Bearer <token>）。
const metricsAuthHeader = "Authorization"

// metricsBearerPrefix 是 Authorization 头中 Bearer 方案的前缀。
const metricsBearerPrefix = "Bearer "

// metricsTokenHeader 是备用口令头，便于 curl/运维工具直接传值。
const metricsTokenHeader = "X-Metrics-Token"

// serveOptions 控制 ServeMetrics 行为。
type serveOptions struct {
	path            string
	token           string
	tokenFromEnv    bool
	shutdownTimeout time.Duration
}

// ServeOption 配置 ServeMetrics。
type ServeOption func(*serveOptions)

// WithPath 自定义抓取路径，默认 /metrics。
func WithPath(path string) ServeOption {
	return func(o *serveOptions) {
		if path != "" {
			o.path = path
		}
	}
}

// WithToken 显式设置 /metrics 鉴权口令。非空即强制校验，优先级高于环境变量。
func WithToken(token string) ServeOption {
	return func(o *serveOptions) { o.token = strings.TrimSpace(token) }
}

// WithoutTokenFromEnv 禁用从 METRICS_TOKEN 环境变量读取口令（默认会读取）。
func WithoutTokenFromEnv() ServeOption {
	return func(o *serveOptions) { o.tokenFromEnv = false }
}

// WithShutdownTimeout 设置优雅停机的等待上限，默认 5s。
func WithShutdownTimeout(d time.Duration) ServeOption {
	return func(o *serveOptions) {
		if d > 0 {
			o.shutdownTimeout = d
		}
	}
}

// resolveToken 计算最终生效的口令：显式 token 优先，其次读环境变量（若未禁用）。
func resolveToken(o *serveOptions) string {
	if o.token != "" {
		return o.token
	}
	if o.tokenFromEnv {
		return strings.TrimSpace(os.Getenv(MetricsTokenEnv))
	}
	return ""
}

// extractMetricsToken 从请求头提取调用方携带的口令，
// 优先 Authorization: Bearer <token>，回退 X-Metrics-Token。
func extractMetricsToken(r *http.Request) string {
	if v := r.Header.Get(metricsAuthHeader); v != "" {
		if strings.HasPrefix(v, metricsBearerPrefix) {
			return strings.TrimSpace(v[len(metricsBearerPrefix):])
		}
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(r.Header.Get(metricsTokenHeader))
}

// authMetrics 给 /metrics handler 套上可选口令鉴权。
// token 为空表示未启用鉴权（直接放行）；非空则用常量时间比对，失败返回 401。
func authMetrics(token string, handler http.Handler) http.Handler {
	if token == "" {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqToken := extractMetricsToken(r)
		if reqToken == "" || subtle.ConstantTimeCompare([]byte(reqToken), []byte(token)) != 1 {
			log.WarnTag("metrics", "/metrics 鉴权失败 remote=%s", r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

// Handler 返回绑定到指定 Provider 的 promhttp 处理器（不含鉴权与服务器生命周期）。
// 适合业务方把 /metrics 挂到自己已有的 http.ServeMux 上。
func Handler(p *Provider) http.Handler {
	return promhttp.HandlerFor(p.Registry(), promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// HandlerWithAuth 返回带可选口令鉴权的 /metrics 处理器，供挂到外部 mux。
// token 为空 = 不鉴权。
func HandlerWithAuth(p *Provider, token string) http.Handler {
	return authMetrics(strings.TrimSpace(token), Handler(p))
}

// ServeMetrics 在 addr 上起一个独立 HTTP 服务暴露 /metrics 端点，
// 阻塞直到 stop 被关闭（或 ListenAndServe 出错），随后优雅停机。
// 返回一个 <-chan error：服务退出时投递最终错误（正常关闭为 nil）后关闭。
//
// 用法（在 main 里）：
//
//	p := prometheus.NewProvider(prometheus.WithNamespace("tgf"))
//	metrics.SetProvider(p)
//	stop := make(chan struct{})
//	done := prometheus.ServeMetrics(p, ":9100", stop)
//	// ... 程序退出时：
//	close(stop)
//	<-done
func ServeMetrics(p *Provider, addr string, stop <-chan struct{}, opts ...ServeOption) <-chan error {
	so := &serveOptions{
		path:            "/metrics",
		tokenFromEnv:    true,
		shutdownTimeout: 5 * time.Second,
	}
	for _, o := range opts {
		o(so)
	}

	token := resolveToken(so)
	mux := http.NewServeMux()
	mux.Handle(so.path, HandlerWithAuth(p, token))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	done := make(chan error, 1)
	authState := "无鉴权"
	if token != "" {
		authState = "已启用口令鉴权"
	}
	log.InfoTag("metrics", "启动 /metrics 端点 addr=%s path=%s（%s）", addr, so.path, authState)

	// 监听 stop 信号，触发优雅停机。
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), so.shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.WarnTag("metrics", "/metrics 端点优雅停机超时/失败: %v", err)
		}
	}()

	go func() {
		err := srv.ListenAndServe()
		if err == http.ErrServerClosed {
			err = nil // 正常关闭
		}
		if err != nil {
			log.ErrorTag("metrics", "/metrics 端点退出异常: %v", err)
		}
		done <- err
		close(done)
	}()

	return done
}
