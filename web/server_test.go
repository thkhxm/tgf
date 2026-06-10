package web

// G1 单测：Server 生命周期——真实端口监听、默认中间件链端到端、
// 优雅停机 drain in-flight、Backend 注入、Options 默认值。

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// startTestServer 启动一个真实监听的 Server（127.0.0.1 随机端口），cleanup 自动停机。
func startTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	srv := NewServer(opts)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv
}

// httpGet 对真实地址发 GET。
func httpGet(t *testing.T, addr, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// TestServer_StartServeShutdown 是 Server 生命周期的端到端用例：
// 真实端口起服务 → 请求命中路由（默认链生效：响应带 traceId 头）→
// Shutdown 后新连接被拒 → Shutdown 幂等。
func TestServer_StartServeShutdown(t *testing.T) {
	srv := startTestServer(t, Options{
		Routes: func(r *Router) {
			r.GET("/ping", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("pong"))
			})
		},
	})
	addr := srv.Addr()
	if addr == "" || addr == "127.0.0.1:0" {
		t.Fatalf("Start 后 Addr 应为内核分配的真实地址, got %q", addr)
	}

	resp, body := httpGet(t, addr, "/ping")
	if resp.StatusCode != http.StatusOK || body != "pong" {
		t.Errorf("GET /ping = (%d, %q), want (200, pong)", resp.StatusCode, body)
	}
	if resp.Header.Get(HeaderTraceID) == "" {
		t.Error("默认中间件链应注入 X-Trace-Id 响应头")
	}

	// 重复 Start 报错
	if err := srv.Start(); err != ErrServerAlreadyStarted {
		t.Errorf("重复 Start = %v, want ErrServerAlreadyStarted", err)
	}

	// Shutdown 后新连接被拒
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown 失败: %v", err)
	}
	if _, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		t.Error("Shutdown 后新连接应被拒绝")
	}
	// 幂等
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Errorf("重复 Shutdown 应为安全 no-op, got %v", err)
	}
}

// TestServer_ShutdownDrainsInflight 验证优雅停机 drain：停机发起时 in-flight
// 请求被处理完（客户端拿到完整 200 响应），Shutdown 等到其完成才返回。
func TestServer_ShutdownDrainsInflight(t *testing.T) {
	const handlerDelay = 300 * time.Millisecond
	var handlerDone atomic.Bool

	srv := startTestServer(t, Options{
		Routes: func(r *Router) {
			r.GET("/slow", func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(handlerDelay)
				handlerDone.Store(true)
				_, _ = w.Write([]byte("done"))
			})
		},
	})
	addr := srv.Addr()

	type result struct {
		status int
		body   string
		err    error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		resCh <- result{status: resp.StatusCode, body: string(b)}
	}()

	// 等请求进入 handler（小于 handler 耗时即可）
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown 应等到 in-flight 完成后正常返回, got %v", err)
	}
	if !handlerDone.Load() {
		t.Error("Shutdown 返回时 in-flight handler 应已执行完")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("Shutdown 返回过快(%v), 疑似没有 drain in-flight 请求", elapsed)
	}

	select {
	case r := <-resCh:
		if r.err != nil {
			t.Fatalf("in-flight 请求在停机时被掐断: %v", r.err)
		}
		if r.status != http.StatusOK || r.body != "done" {
			t.Errorf("in-flight 响应 = (%d, %q), want (200, done)", r.status, r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待 in-flight 响应超时")
	}
}

// TestServer_ShutdownTimeoutGivesUp 验证超长 handler 超出 drain 超时后
// Shutdown 返回 ctx 错误（停机不被卡死——D3 编排里只告警继续）。
func TestServer_ShutdownTimeoutGivesUp(t *testing.T) {
	release := make(chan struct{})
	srv := startTestServer(t, Options{
		Routes: func(r *Router) {
			r.GET("/hang", func(w http.ResponseWriter, _ *http.Request) {
				<-release
				_, _ = w.Write([]byte("late"))
			})
		},
	})
	addr := srv.Addr()

	errCh := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/hang")
		if resp != nil {
			resp.Body.Close()
		}
		errCh <- err
	}()
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); err == nil {
		t.Error("drain 超时应返回非 nil 错误")
	}

	// 放行挂死的 handler（Shutdown 超时只是放弃等待，连接/goroutine 仍在），
	// 再等客户端请求结束——避免测试自身泄漏 goroutine 卡住整个测试进程。
	close(release)
	select {
	case <-errCh: // 不校验结果：超时放弃后连接的最终状态取决于 handler 何时返回
	case <-time.After(5 * time.Second):
		t.Fatal("放行 handler 后客户端请求应当结束")
	}
}

// TestServer_BackendInjectionEndToEnd 验证 Options.Backend 注入：
// handler 经 BackendFromRequest 拿到注入实现并完成调用——这是 rpc 侧
// （WithHTTPService 默认后端）与 G2 桥共同依赖的注入契约。
func TestServer_BackendInjectionEndToEnd(t *testing.T) {
	var gotModule, gotMethod string
	backend := BackendFunc(func(_ context.Context, module, method string, args, reply any) error {
		gotModule, gotMethod = module, method
		*(reply.(*string)) = "backend:" + *(args.(*string))
		return nil
	})

	srv := startTestServer(t, Options{
		Backend: backend,
		Routes: func(r *Router) {
			r.GET("/call/{msg}", func(w http.ResponseWriter, req *http.Request) {
				b, ok := BackendFromRequest(req)
				if !ok {
					http.Error(w, "no backend", http.StatusInternalServerError)
					return
				}
				args := req.PathValue("msg")
				var reply string
				if err := b.Invoke(req.Context(), "user", "Echo", &args, &reply); err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				_, _ = w.Write([]byte(reply))
			})
		},
	})

	resp, body := httpGet(t, srv.Addr(), "/call/hello")
	if resp.StatusCode != http.StatusOK || body != "backend:hello" {
		t.Errorf("Backend 注入调用 = (%d, %q), want (200, backend:hello)", resp.StatusCode, body)
	}
	if gotModule != "user" || gotMethod != "Echo" {
		t.Errorf("Backend 收到 module.method = %s.%s, want user.Echo", gotModule, gotMethod)
	}
}

// TestServer_AuthGroupIntegration 验证服务级装配下的分组鉴权：
// /admin 组 fail-closed / 401 / 通过；开放路由不受影响。
func TestServer_AuthGroupIntegration(t *testing.T) {
	srv := startTestServer(t, Options{
		Routes: func(r *Router) {
			r.GET("/open", okHandlerFunc)
			admin := r.Group("/admin", Auth(StaticBearerToken("tok")))
			admin.GET("/stats", okHandlerFunc)
		},
	})
	addr := srv.Addr()

	if resp, _ := httpGet(t, addr, "/open"); resp.StatusCode != http.StatusOK {
		t.Errorf("开放路由 = %d, want 200", resp.StatusCode)
	}
	if resp, _ := httpGet(t, addr, "/admin/stats"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("无凭据访问受保护路由 = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("带凭据请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("正确凭据 = %d, want 200", resp.StatusCode)
	}
}

// TestNewServer_DefaultsApplied 验证 Options 零值归一化。
func TestNewServer_DefaultsApplied(t *testing.T) {
	srv := NewServer(Options{})
	if srv.opts.Addr != DefaultAddr {
		t.Errorf("默认 Addr = %q, want %q", srv.opts.Addr, DefaultAddr)
	}
	if srv.opts.ReadHeaderTimeout != DefaultReadHeaderTimeout {
		t.Errorf("默认 ReadHeaderTimeout = %v, want %v", srv.opts.ReadHeaderTimeout, DefaultReadHeaderTimeout)
	}
	if srv.opts.IdleTimeout != DefaultIdleTimeout {
		t.Errorf("默认 IdleTimeout = %v, want %v", srv.opts.IdleTimeout, DefaultIdleTimeout)
	}
	if srv.ShutdownTimeout() != DefaultShutdownTimeout {
		t.Errorf("默认 ShutdownTimeout = %v, want %v", srv.ShutdownTimeout(), DefaultShutdownTimeout)
	}
	// 未 Start 的 Shutdown 是安全 no-op
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Errorf("未 Start 的 Shutdown 应为 no-op, got %v", err)
	}
}

// TestServer_RouterAccessorAddRouteBeforeStart 验证 NewServer 之后、Start 之前
// 仍可经 Router() 注册路由。
func TestServer_RouterAccessorAddRouteBeforeStart(t *testing.T) {
	srv := NewServer(Options{Addr: "127.0.0.1:0"})
	srv.Router().GET("/late", okHandlerFunc)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	if resp, body := httpGet(t, srv.Addr(), "/late"); resp.StatusCode != http.StatusOK || body != "ok" {
		t.Errorf("Router() 后注册路由 = (%d, %q), want (200, ok)", resp.StatusCode, body)
	}
}
