//go:build integration
// +build integration

package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description G2/G3 · 真实 Consul 端到端验收（testcontainers）
//
//  1. client-only 调通后端（G3 验收）：后端 Server 注册进 Consul；client-only
//     Server（不注册任何 service、不监听 rpcx 端口）经 HTTP 桥（web.RPC）→
//     默认 Backend → Consul 发现 → 真实 rpcx 网络 → 后端 service → JSON 返回；
//     traceId 跨进程边界（HTTP 头 → rpcx ReqMetaData → 后端 ctx）全程一致。
//  2. HTTP 服务注册进 Consul（G2/分布式 web 验收）：WithHTTPServiceConsul 注册
//     真实 agent service + TTL check；注册即 passing 且续约维持跨 TTL 周期；
//     health endpoint 真实可探；Consul Health API（标准 LB 发现入口）能查到
//     该节点；Destroy 后 service 摘除。
//
//2026/6/10
//***************************************************

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/trace"
	"github.com/thkhxm/tgf/web"
)

// ItG3EchoReq / ItG3EchoRes 必须是导出类型——真实 rpcx RegisterName 要求
// 方法参数类型 exported（与单进程反射直通不同）。
type ItG3EchoReq struct {
	Msg string `json:"msg"`
}

type ItG3EchoRes struct {
	Echo    string `json:"echo"`
	TraceId string `json:"traceId"`
}

// itG3EchoService 后端回显服务：返回入参 + 服务端看到的链路 traceId。
type itG3EchoService struct {
	Module
	lastTraceID atomic.Value // string
}

func (s *itG3EchoService) GetName() string        { return "it-g3-echo" }
func (s *itG3EchoService) GetVersion() string     { return "1.0" }
func (s *itG3EchoService) Startup() (bool, error) { return true, nil }

func (s *itG3EchoService) Echo(ctx context.Context, args *ItG3EchoReq, reply *ItG3EchoRes) error {
	tid := trace.TraceIDFromContext(ctx)
	s.lastTraceID.Store(tid)
	reply.Echo = "echo:" + args.Msg
	reply.TraceId = tid
	return nil
}

// itGSetup IT 公共环境：关数据缓存层（用例不依赖 Redis/MySQL）+ 隔离 dispatcher。
func itGSetup(t *testing.T) *rpcITEnv {
	t.Helper()
	env := ensureRPCIT(t)
	db.WithCacheModule(tgf.CacheModuleClose)
	t.Cleanup(func() { db.WithCacheModule(tgf.CacheModuleRedis) })
	ResetLocalDispatcherForTest()
	t.Cleanup(ResetLocalDispatcherForTest)
	return env
}

// itGPostJSON 对真实地址发 POST JSON，返回 (status, body, 响应头 traceId, err)。
func itGPostJSON(addr, path, body string, header map[string]string) (int, string, string, error) {
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	cli := &http.Client{Timeout: 5 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get(web.HeaderTraceID), nil
}

// TestIT_G3_ClientOnly_BridgeCallsBackend G3 核心验收：client-only 进程
// （不注册 service、不监听 rpcx 端口）经 HTTP 桥真实调通 Consul 发现的后端。
func TestIT_G3_ClientOnly_BridgeCallsBackend(t *testing.T) {
	itGSetup(t)

	// ---- 后端：注册进 Consul 的真实 rpcx 服务节点 ----
	backendPort := freeITPort(t)
	setITServicePort(t, backendPort)
	echo := &itG3EchoService{}
	backend := NewRPCServer()
	backend.WithCustomServiceAddress() // ServiceAddress=127.0.0.1（harness 已设）
	backend.WithoutServiceClient()     // 后端不抢全局 rpcClient——client 初始化必须由 client-only 进程完成
	backend.WithService(echo)
	if backend.Run() == nil {
		t.Fatal("后端 Run 应返回关闭通知通道")
	}
	t.Cleanup(backend.Destroy)

	// ---- client-only：HTTP 入口 + RPC client，仅此而已 ----
	clientOnly := NewRPCServer()
	clientOnly.WithClientOnly()
	clientOnly.WithHTTPService(web.Options{
		Addr: "127.0.0.1:0",
		Routes: func(r *web.Router) {
			r.POST("/api/echo", web.RPC[ItG3EchoReq, ItG3EchoRes]("it-g3-echo", "Echo"))
		},
	})
	if clientOnly.Run() == nil {
		t.Fatal("client-only Run 应返回关闭通知通道")
	}
	t.Cleanup(clientOnly.Destroy)

	// G3 形态契约：不伪装成 RPC 节点。
	if clientOnly.rpcServer != nil {
		t.Error("client-only 不应创建 rpcx server")
	}
	httpAddr := clientOnly.httpServers[0].Addr()

	// ---- 端到端：HTTP → 桥 → Consul 发现 → rpcx 网络 → 后端 ----
	// 发现传播是异步的（client watch + selector UpdateServer），轮询至打通。
	var lastBody string
	rpcITWaitUntil(t, 30*time.Second, "client-only 经桥调通后端", func() bool {
		status, body, _, err := itGPostJSON(httpAddr, "/api/echo", `{"msg":"hi"}`, nil)
		lastBody = fmt.Sprintf("status=%d body=%s err=%v", status, body, err)
		return err == nil && status == http.StatusOK && strings.Contains(body, "echo:hi")
	})
	t.Logf("打通后的最后响应: %s", lastBody)

	// ---- traceId 跨进程一致：HTTP 头 → rpcx ReqMetaData → 后端 ctx ----
	const wantTID = "it-g3-trace-1"
	status, body, respTID, err := itGPostJSON(httpAddr, "/api/echo", `{"msg":"trace"}`,
		map[string]string{web.HeaderTraceID: wantTID})
	if err != nil || status != http.StatusOK {
		t.Fatalf("trace 请求失败: status=%d err=%v body=%s", status, err, body)
	}
	if respTID != wantTID {
		t.Errorf("响应头 traceId = %q, want %q", respTID, wantTID)
	}
	var res ItG3EchoRes
	if jerr := json.Unmarshal([]byte(body), &res); jerr != nil {
		t.Fatalf("响应解码失败: %v body=%s", jerr, body)
	}
	if res.TraceId != wantTID {
		t.Errorf("后端 ctx traceId = %q, want %q（跨 rpcx 网络透传）", res.TraceId, wantTID)
	}
	if got, _ := echo.lastTraceID.Load().(string); got != wantTID {
		t.Errorf("后端记录的 traceId = %q, want %q", got, wantTID)
	}

	// ---- 停机：client-only Destroy 后 HTTP 端口关闭，后端不受影响 ----
	clientOnly.Destroy()
	if conn, derr := net.DialTimeout("tcp", httpAddr, 500*time.Millisecond); derr == nil {
		conn.Close()
		t.Error("client-only Destroy 后 HTTP 端口应已关闭")
	}
	if conn, derr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", backendPort), time.Second); derr != nil {
		t.Errorf("后端不应受 client-only 停机影响: %v", derr)
	} else {
		conn.Close()
	}
}

// TestIT_G2_HTTPService_ConsulRegisterDiscoverDeregister G2 分布式 web 验收：
// HTTP 服务注册进 Consul（TTL check）→ passing 维持跨 TTL 周期 → health
// endpoint 真实可探 → Consul Health API 可发现 → Destroy 摘除。
func TestIT_G2_HTTPService_ConsulRegisterDiscoverDeregister(t *testing.T) {
	env := itGSetup(t)

	s := NewRPCServer()
	s.WithClientOnly() // 纯 web 进程形态：HTTP 入口 + client，自身只以 HTTP service 进入发现
	s.WithHTTPServiceConsul(web.Options{
		Addr: "127.0.0.1:0",
		Routes: func(r *web.Router) {
			r.GET("/api/hello", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("hello"))
			})
		},
	}, HTTPRegistration{
		ServiceName: "it-g2-web",
		Interval:    time.Second, // TTL = 3s
	})
	if s.Run() == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)

	httpAddr := s.httpServers[0].Addr()
	serviceID := fmt.Sprintf("it-g2-web-%s", httpAddr)
	checkID := serviceID + "-check"

	apiCli, err := api.NewClient(&api.Config{Address: env.consulAddr})
	if err != nil {
		t.Fatalf("创建 consul api client 失败: %v", err)
	}

	// ---- 1: agent service 已注册且 TTL check passing ----
	rpcITWaitUntil(t, 15*time.Second, "HTTP service 注册且 check passing", func() bool {
		checks, cerr := apiCli.Agent().Checks()
		if cerr != nil {
			return false
		}
		c, ok := checks[checkID]
		return ok && c.Status == api.HealthPassing
	})

	// 跨 TTL 周期仍 passing：证明是续约 goroutine 在维持，不是注册初值的余晖。
	time.Sleep(4 * time.Second) // > TTL 3s
	checks, cerr := apiCli.Agent().Checks()
	if cerr != nil {
		t.Fatalf("查询 agent checks 失败: %v", cerr)
	}
	if c, ok := checks[checkID]; !ok || c.Status != api.HealthPassing {
		t.Fatalf("跨 TTL 周期后 check 应仍 passing(续约维持), got %+v", c)
	}

	// ---- 2: health endpoint 真实可探（自动挂载的 /health）----
	resp, herr := http.Get("http://" + httpAddr + web.DefaultHealthPath)
	if herr != nil {
		t.Fatalf("GET /health 失败: %v", herr)
	}
	hb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(hb), `"ok"`) {
		t.Fatalf("GET /health = (%d, %s), want 200 ok", resp.StatusCode, hb)
	}

	// ---- 3: 标准发现入口（Consul Health API，LB/网关都走它）能查到本节点 ----
	entries, _, qerr := apiCli.Health().Service("it-g2-web", "", true, nil)
	if qerr != nil {
		t.Fatalf("Health().Service 查询失败: %v", qerr)
	}
	if len(entries) != 1 {
		t.Fatalf("健康节点数 = %d, want 1", len(entries))
	}
	gotAddr := fmt.Sprintf("%s:%d", entries[0].Service.Address, entries[0].Service.Port)
	if gotAddr != httpAddr {
		t.Errorf("发现的地址 = %q, want %q", gotAddr, httpAddr)
	}
	if entries[0].Service.Meta["protocol"] != "http" || entries[0].Service.Meta["healthPath"] != web.DefaultHealthPath {
		t.Errorf("service Meta = %v, want protocol=http healthPath=/health", entries[0].Service.Meta)
	}
	// 发现的地址真实可服务（"可被发现 ⇒ 必然可连接"）。
	resp2, herr2 := http.Get("http://" + gotAddr + "/api/hello")
	if herr2 != nil {
		t.Fatalf("经发现地址请求失败: %v", herr2)
	}
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || string(b2) != "hello" {
		t.Errorf("经发现地址 = (%d, %s), want (200, hello)", resp2.StatusCode, b2)
	}

	// ---- 4: Destroy → service 从 agent 与发现结果双双摘除 ----
	s.Destroy()
	rpcITWaitUntil(t, 10*time.Second, "Destroy 后 agent service 摘除", func() bool {
		services, serr := apiCli.Agent().Services()
		if serr != nil {
			return false
		}
		_, ok := services[serviceID]
		return !ok
	})
	rpcITWaitUntil(t, 10*time.Second, "Destroy 后发现结果为空", func() bool {
		es, _, e := apiCli.Health().Service("it-g2-web", "", true, nil)
		return e == nil && len(es) == 0
	})
}
