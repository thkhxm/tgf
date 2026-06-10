package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description G1 端到端测试：WithHTTPService 的 rpc 侧接线——
//  1. Run() 启动 HTTP 监听，路由/中间件链生效（X-Trace-Id 注入）；
//  2. 默认 Backend（defaultWebBackend）打通 HTTP handler → 后端 service：
//     单进程直通路径 + traceId 全链路一致（HTTP X-Trace-Id == 服务端 ctx 内
//     rpcx ReqMetaData["TraceId"]）；
//  3. 未注册模块走 SendRPCMessageByStr 回退路径返回干净错误（不 panic）；
//  4. Destroy() 把 HTTP 服务挂进 D3 优雅停机序列：in-flight 请求 drain 完成、
//     新连接被拒、幂等；
//  5. Addr 省略时从配置 HTTPPort（E2 配置系统）取端口。
//
//2026/6/10
//***************************************************

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/trace"
	"github.com/thkhxm/tgf/web"
)

// g1EchoService 是 G1 用例的最小后端服务：回显入参并捕获链路 traceId。
type g1EchoService struct {
	Module
	lastTraceID atomic.Value // string：Echo 被调时 ctx 内的 traceId
}

func (s *g1EchoService) GetName() string        { return "g1echo" }
func (s *g1EchoService) GetVersion() string     { return "1.0" }
func (s *g1EchoService) Startup() (bool, error) { return true, nil }

func (s *g1EchoService) Echo(ctx context.Context, args *string, reply *string) error {
	s.lastTraceID.Store(trace.TraceIDFromContext(ctx))
	*reply = "echo:" + *args
	return nil
}

// g1Setup 公共环境：关数据缓存层（Run 会触发 db.Run）、隔离 local dispatcher、
// 给 rpcx Serve 配空闲端口。
func g1Setup(t *testing.T) {
	t.Helper()
	db.WithCacheModule(tgf.CacheModuleClose)
	t.Cleanup(func() { db.WithCacheModule(tgf.CacheModuleRedis) })
	ResetLocalDispatcherForTest()
	t.Cleanup(ResetLocalDispatcherForTest)
	setEnvConfigForTest(t, "ServicePort", fmt.Sprintf("%d", freeF2Port(t)))
}

// g1Get 对真实地址发 GET（可带请求头），返回响应与 body。
func g1Get(t *testing.T, addr, path string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// TestG1_HTTPService_EndToEnd_SingleProcess 是 G1 的核心验收用例（消费方：
// Server.Run startHTTPServers + defaultWebBackend + Server.Destroy
// shutdownHTTPServers）：单进程模式下真实起 HTTP 服务，HTTP 请求经默认
// Backend 直通后端 service，traceId 全链路一致；Destroy 后端口关闭。
func TestG1_HTTPService_EndToEnd_SingleProcess(t *testing.T) {
	g1Setup(t)

	echo := &g1EchoService{}
	s := NewRPCServer()
	s.WithSingleProcess()
	s.WithService(echo)
	s.WithHTTPService(web.Options{
		Addr: "127.0.0.1:0",
		Routes: func(r *web.Router) {
			r.GET("/echo/{msg}", func(w http.ResponseWriter, req *http.Request) {
				backend, ok := web.BackendFromRequest(req)
				if !ok {
					http.Error(w, "no backend", http.StatusInternalServerError)
					return
				}
				args := req.PathValue("msg")
				var reply string
				if err := backend.Invoke(req.Context(), "g1echo", "Echo", &args, &reply); err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				_, _ = w.Write([]byte(reply))
			})
			r.GET("/missing", func(w http.ResponseWriter, req *http.Request) {
				backend, _ := web.BackendFromRequest(req)
				var reply string
				args := "x"
				// 未注册模块：必须返回干净错误（SendRPCMessageByStr 回退路径），不 panic
				if err := backend.Invoke(req.Context(), "g1-not-registered", "Echo", &args, &reply); err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				_, _ = w.Write([]byte(reply))
			})
		},
	})

	done := s.Run()
	if done == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)

	if len(s.httpServers) != 1 {
		t.Fatalf("Run 后 httpServers 数量 = %d, want 1", len(s.httpServers))
	}
	addr := s.httpServers[0].Addr()

	// ---- 1+2: 路由 + 默认 Backend 直通 + traceId 全链路一致 ----
	resp, body := g1Get(t, addr, "/echo/hello", map[string]string{web.HeaderTraceID: "g1-trace-123"})
	if resp.StatusCode != http.StatusOK || body != "echo:hello" {
		t.Fatalf("GET /echo/hello = (%d, %q), want (200, echo:hello)", resp.StatusCode, body)
	}
	if got := resp.Header.Get(web.HeaderTraceID); got != "g1-trace-123" {
		t.Errorf("响应 X-Trace-Id = %q, want g1-trace-123（入站 id 应透传）", got)
	}
	if got, _ := echo.lastTraceID.Load().(string); got != "g1-trace-123" {
		t.Errorf("后端 service ctx 内 traceId = %q, want g1-trace-123（HTTP→RPC 链路应同 id）", got)
	}

	// 不带入站 traceId：中间件生成新 id，链路两端仍一致
	resp, body = g1Get(t, addr, "/echo/auto", nil)
	if resp.StatusCode != http.StatusOK || body != "echo:auto" {
		t.Fatalf("GET /echo/auto = (%d, %q), want (200, echo:auto)", resp.StatusCode, body)
	}
	genTID := resp.Header.Get(web.HeaderTraceID)
	if genTID == "" {
		t.Error("未带入站 traceId 时应生成新 id 并回写响应头")
	}
	if got, _ := echo.lastTraceID.Load().(string); got != genTID {
		t.Errorf("生成的 traceId 链路两端不一致: 服务端 %q, 响应头 %q", got, genTID)
	}

	// ---- 3: 未注册模块走分布式回退路径，干净错误不 panic ----
	resp, body = g1Get(t, addr, "/missing", nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("未注册模块 = (%d, %q), want 502（干净错误而非 panic/挂死）", resp.StatusCode, body)
	}

	// ---- 4: Destroy 关闭 HTTP 端口 + 幂等 ----
	s.Destroy()
	if conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		conn.Close()
		t.Error("Destroy 后 HTTP 端口应已关闭")
	}
	s.Destroy() // 幂等：不得 panic
}

// TestG1_HTTPService_DestroyDrainsInflight 验证 HTTP 服务真实挂进 D3 优雅停机
// 序列：Destroy 期间 in-flight HTTP 请求被 drain 完成（客户端拿到完整 200），
// 而不是被掐断。
func TestG1_HTTPService_DestroyDrainsInflight(t *testing.T) {
	g1Setup(t)

	const handlerDelay = 300 * time.Millisecond
	s := NewRPCServer()
	s.WithStandalone()
	s.WithShutdownDrainTimeout(2 * time.Second)
	s.WithHTTPService(web.Options{
		Addr: "127.0.0.1:0",
		Routes: func(r *web.Router) {
			r.GET("/slow", func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(handlerDelay)
				_, _ = w.Write([]byte("done"))
			})
		},
	})
	if s.Run() == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)
	addr := s.httpServers[0].Addr()

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

	time.Sleep(100 * time.Millisecond) // 请求已进入 handler
	s.Destroy()                        // 阻塞直到停机序列完成（含 HTTP drain）

	select {
	case r := <-resCh:
		if r.err != nil {
			t.Fatalf("Destroy 应 drain in-flight HTTP 请求, 实际被掐断: %v", r.err)
		}
		if r.status != http.StatusOK || r.body != "done" {
			t.Errorf("in-flight 响应 = (%d, %q), want (200, done)", r.status, r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待 in-flight 响应超时")
	}

	if conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		conn.Close()
		t.Error("Destroy 后 HTTP 端口应已关闭")
	}
}

// TestG1_HTTPService_AddrFromConfig 验证 Addr 省略时从配置系统（E2）取
// HTTPPort：WithHTTPService(web.Options{Addr: ""}) → 监听 ":<HTTPPort>"。
func TestG1_HTTPService_AddrFromConfig(t *testing.T) {
	g1Setup(t)

	httpPort := freeF2Port(t)
	setEnvConfigForTest(t, "HTTPPort", fmt.Sprintf("%d", httpPort))

	s := NewRPCServer()
	s.WithStandalone()
	s.WithHTTPService(web.Options{
		Routes: func(r *web.Router) {
			r.GET("/ping", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("pong"))
			})
		},
	})
	if s.Run() == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)

	wantSuffix := fmt.Sprintf(":%d", httpPort)
	addr := s.httpServers[0].Addr()
	if len(addr) < len(wantSuffix) || addr[len(addr)-len(wantSuffix):] != wantSuffix {
		t.Errorf("HTTP 监听地址 = %q, 端口应来自配置 HTTPPort=%d", addr, httpPort)
	}

	resp, body := g1Get(t, fmt.Sprintf("127.0.0.1:%d", httpPort), "/ping", nil)
	if resp.StatusCode != http.StatusOK || body != "pong" {
		t.Errorf("经配置端口请求 = (%d, %q), want (200, pong)", resp.StatusCode, body)
	}
}

// TestG1_ApplyHTTPDefaults 验证配置默认值填充逻辑（不起服务的纯单元面）。
func TestG1_ApplyHTTPDefaults(t *testing.T) {
	setEnvConfigForTest(t, "HTTPPort", "18099")
	setEnvConfigForTest(t, "HTTPReadHeaderTimeoutSec", "7")
	setEnvConfigForTest(t, "HTTPShutdownTimeoutSec", "13")

	opt := web.Options{}
	applyHTTPDefaults(&opt)
	if opt.Addr != ":18099" {
		t.Errorf("Addr = %q, want :18099", opt.Addr)
	}
	if opt.ReadHeaderTimeout != 7*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 7s", opt.ReadHeaderTimeout)
	}
	if opt.ShutdownTimeout != 13*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 13s", opt.ShutdownTimeout)
	}

	// 显式值不被配置覆盖
	opt2 := web.Options{Addr: "127.0.0.1:9999", ReadHeaderTimeout: time.Second, ShutdownTimeout: 2 * time.Second}
	applyHTTPDefaults(&opt2)
	if opt2.Addr != "127.0.0.1:9999" || opt2.ReadHeaderTimeout != time.Second || opt2.ShutdownTimeout != 2*time.Second {
		t.Errorf("显式 Options 不应被配置覆盖: %+v", opt2)
	}
}
