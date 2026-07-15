package prometheus

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHandler_ReturnsPrometheusText 验证 /metrics handler 返回 prometheus 文本格式，
// 且包含已注册指标的 HELP/TYPE 注释行与样本行。
func TestHandler_ReturnsPrometheusText(t *testing.T) {
	p := NewProvider(WithNamespace("tgf"))
	p.Counter("demo_total", "演示计数").Add(42)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	Handler(p).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码期望 200, 实际 %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type 期望 prometheus 文本, 实际 %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# HELP tgf_demo_total 演示计数",
		"# TYPE tgf_demo_total counter",
		"tgf_demo_total 42",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("响应体缺少 %q\n实际:\n%s", want, body)
		}
	}
}

// TestHandlerWithAuth_NoToken 未配置口令时直接放行。
func TestHandlerWithAuth_NoToken(t *testing.T) {
	p := NewProvider()
	p.Counter("x", "h").Inc()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	HandlerWithAuth(p, "").ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("无口令应放行, 期望 200, 实际 %d", rec.Code)
	}
}

// TestHandlerWithAuth_TokenMatrix 表驱动验证带口令时的鉴权矩阵。
func TestHandlerWithAuth_TokenMatrix(t *testing.T) {
	const secret = "s3cr3t"
	p := NewProvider()
	p.Counter("x", "h").Inc()

	cases := []struct {
		name       string
		authHeader string // Authorization 头
		tokenHdr   string // X-Metrics-Token 头
		wantCode   int
	}{
		{name: "不带任何口令_401", wantCode: http.StatusUnauthorized},
		{name: "Bearer正确_200", authHeader: "Bearer " + secret, wantCode: http.StatusOK},
		{name: "Bearer错误_401", authHeader: "Bearer wrong", wantCode: http.StatusUnauthorized},
		{name: "Authorization裸值正确_200", authHeader: secret, wantCode: http.StatusOK},
		{name: "X-Metrics-Token正确_200", tokenHdr: secret, wantCode: http.StatusOK},
		{name: "X-Metrics-Token错误_401", tokenHdr: "nope", wantCode: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			if tc.tokenHdr != "" {
				req.Header.Set(metricsTokenHeader, tc.tokenHdr)
			}
			HandlerWithAuth(p, secret).ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("状态码期望 %d, 实际 %d", tc.wantCode, rec.Code)
			}
		})
	}
}

// TestResolveToken 验证口令来源优先级：显式 token > 环境变量 > 空。
func TestResolveToken(t *testing.T) {
	t.Run("显式token优先", func(t *testing.T) {
		t.Setenv(MetricsTokenEnv, "from-env")
		got := resolveToken(&serveOptions{token: "explicit", tokenFromEnv: true})
		if got != "explicit" {
			t.Fatalf("期望 explicit, 实际 %q", got)
		}
	})
	t.Run("回退环境变量", func(t *testing.T) {
		t.Setenv(MetricsTokenEnv, "  from-env  ")
		got := resolveToken(&serveOptions{tokenFromEnv: true})
		if got != "from-env" {
			t.Fatalf("应裁剪空白并取环境变量, 期望 from-env, 实际 %q", got)
		}
	})
	t.Run("禁用环境变量则为空", func(t *testing.T) {
		t.Setenv(MetricsTokenEnv, "from-env")
		got := resolveToken(&serveOptions{tokenFromEnv: false})
		if got != "" {
			t.Fatalf("禁用 env 后应为空, 实际 %q", got)
		}
	})
}

// TestServeMetrics_EndToEnd 起真实 HTTP 服务、抓取一次、再优雅停机，
// 验证端到端可用（含 stop 信号触发的 graceful shutdown）。
func TestServeMetrics_EndToEnd(t *testing.T) {
	// 选一个空闲端口。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法分配端口: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // 立即释放，交给 ServeMetrics 监听

	p := NewProvider(WithNamespace("tgf"))
	p.Counter("served_total", "h").Add(9)

	stop := make(chan struct{})
	done := ServeMetrics(p, addr, stop, WithoutTokenFromEnv())

	// 轮询直到服务就绪（最多 ~2s）。
	url := "http://" + addr + "/metrics"
	var body string
	var lastErr error
	for i := 0; i < 100; i++ {
		resp, err := http.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(20 * time.Millisecond)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			body = string(b)
			break
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	if body == "" {
		t.Fatalf("/metrics 端点未就绪: %v", lastErr)
	}
	if !strings.Contains(body, "tgf_served_total 9") {
		t.Fatalf("响应体缺少 tgf_served_total 9\n实际:\n%s", body)
	}

	// 触发优雅停机并等待退出。
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅停机应返回 nil, 实际 %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatalf("ServeMetrics 未在超时内退出")
	}
}

// TestServeMetrics_AuthEndToEnd 端到端验证带口令时未授权返回 401。
func TestServeMetrics_AuthEndToEnd(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法分配端口: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	p := NewProvider()
	p.Counter("x", "h").Inc()

	stop := make(chan struct{})
	done := ServeMetrics(p, addr, stop, WithToken("topsecret"), WithoutTokenFromEnv())
	defer func() {
		close(stop)
		<-done
	}()

	url := "http://" + addr + "/metrics"

	// 等就绪：第一次 401 也算服务起来了。
	var ready bool
	for i := 0; i < 100; i++ {
		resp, requestErr := http.Get(url)
		if requestErr == nil {
			_ = resp.Body.Close()
			ready = true
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("无口令应 401, 实际 %d", resp.StatusCode)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("/metrics 端点未就绪")
	}

	// 带正确口令应 200。
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer topsecret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("带口令请求失败: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("带正确口令应 200, 实际 %d", resp.StatusCode)
	}
}
