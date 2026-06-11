package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description G2/G3 单测：HTTP 服务注册进 Consul 的接线（消费方：Server.Run →
// registerHTTPConsulServices → 续约 goroutine → Destroy →
// deregisterHTTPConsulServices）。用注入桩（newHTTPConsulServiceFn）在无 Consul
// 的干净环境下确定性锁住契约：
//  1. 注册发生在 HTTP 监听就绪之后，注册地址 = 实际监听地址（":0" 注册真实端口）；
//  2. health 路由自动挂载（默认 web.Health / 业务自检 web.HealthFunc）；
//  3. TTL 模式续约 goroutine 周期 Renew，Destroy 停续约 + 反注册；
//  4. HTTP check 模式不起续约 goroutine；
//  5. 注册失败只告警不阻断启动；client-only 模式同样走完整注册路径。
//
// 真实 Consul 的注册/发现/摘除端到端由 it_g_http_test.go integration 覆盖。
//2026/6/10
//***************************************************

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thkhxm/tgf/v2/rpc/internal"
	"github.com/thkhxm/tgf/v2/web"
)

// g2FakeHTTPConsul 是 httpConsulHandle 的录制桩。
type g2FakeHTTPConsul struct {
	opts        internal.HTTPServiceOptions
	needsRenew  bool
	renews      atomic.Int64
	deregisters atomic.Int64
}

func (f *g2FakeHTTPConsul) NeedsRenew() bool        { return f.needsRenew }
func (f *g2FakeHTTPConsul) Renew(string) error      { f.renews.Add(1); return nil }
func (f *g2FakeHTTPConsul) Interval() time.Duration { return 20 * time.Millisecond }
func (f *g2FakeHTTPConsul) Deregister() error       { f.deregisters.Add(1); return nil }
func (f *g2FakeHTTPConsul) ServiceID() string       { return f.opts.ServiceName + "-fake" }

// injectFakeHTTPConsul 替换注册注入点，录制每次注册的 options。
func injectFakeHTTPConsul(t *testing.T) *[]*g2FakeHTTPConsul {
	t.Helper()
	created := make([]*g2FakeHTTPConsul, 0, 2)
	orig := newHTTPConsulServiceFn
	newHTTPConsulServiceFn = func(opt internal.HTTPServiceOptions) (httpConsulHandle, error) {
		f := &g2FakeHTTPConsul{opts: opt, needsRenew: opt.CheckMode != internal.HTTPCheckModeHTTP}
		created = append(created, f)
		return f, nil
	}
	t.Cleanup(func() { newHTTPConsulServiceFn = orig })
	return &created
}

// stubDiscoveryForHTTPConsul 装上 F2 同款桩 discovery，让 Run 走"有发现"分支
// （registerHTTPConsulServices 只在 !disableConsul 时执行）。
func stubDiscoveryForHTTPConsul(t *testing.T) {
	t.Helper()
	var startupDone atomic.Bool
	startupDone.Store(true)
	internal.SetDiscoveryForTest(&f2StubDiscovery{plugin: newF2RecorderPlugin(&startupDone)})
	t.Cleanup(internal.ResetDiscoveryForTest)
}

// TestG2_HTTPConsul_RegisterRenewDeregister TTL 模式全生命周期接线：
// Run 注册（真实监听地址）→ health 路由自动挂载 → 续约 goroutine 周期 Renew →
// Destroy 停续约 + 反注册。混用的普通 WithHTTPService 实例不注册。
func TestG2_HTTPConsul_RegisterRenewDeregister(t *testing.T) {
	g1Setup(t)
	stubDiscoveryForHTTPConsul(t)
	created := injectFakeHTTPConsul(t)

	s := NewRPCServer()
	s.WithoutServiceClient()
	s.WithHTTPServiceConsul(web.Options{
		Addr: "127.0.0.1:0",
		Routes: func(r *web.Router) {
			r.GET("/api/ping", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("pong"))
			})
		},
	}, HTTPRegistration{
		ServiceName: "g2-web",
		Interval:    20 * time.Millisecond,
		Tags:        []string{"v1"},
		Meta:        map[string]string{"env": "test"},
	})
	// 混用：第二个 HTTP 实例不带 Consul 注册。
	s.WithHTTPService(web.Options{Addr: "127.0.0.1:0"})

	if s.Run() == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)

	// ---- 1: 只注册声明过的实例，注册地址 = 实际监听地址 ----
	if len(*created) != 1 {
		t.Fatalf("Consul 注册次数 = %d, want 1(普通 WithHTTPService 不注册)", len(*created))
	}
	fake := (*created)[0]
	if fake.opts.ServiceName != "g2-web" {
		t.Errorf("ServiceName = %q, want g2-web", fake.opts.ServiceName)
	}
	if want := s.httpServers[0].Addr(); fake.opts.Address != want {
		t.Errorf("注册地址 = %q, want 实际监听地址 %q(':0' 应注册真实端口)", fake.opts.Address, want)
	}
	if fake.opts.HealthPath != web.DefaultHealthPath {
		t.Errorf("HealthPath = %q, want %q", fake.opts.HealthPath, web.DefaultHealthPath)
	}
	if fake.opts.CheckMode != internal.HTTPCheckModeTTL {
		t.Errorf("CheckMode = %q, want ttl(默认)", fake.opts.CheckMode)
	}
	if len(fake.opts.Tags) != 1 || fake.opts.Tags[0] != "v1" || fake.opts.Meta["env"] != "test" {
		t.Errorf("Tags/Meta 未透传: tags=%v meta=%v", fake.opts.Tags, fake.opts.Meta)
	}

	// ---- 2: health 路由自动挂载（用户路由不受影响）----
	addr := s.httpServers[0].Addr()
	resp, body := g1Get(t, addr, web.DefaultHealthPath, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health = (%d, %s), want 200(自动挂载)", resp.StatusCode, body)
	}
	resp, body = g1Get(t, addr, "/api/ping", nil)
	if resp.StatusCode != http.StatusOK || body != "pong" {
		t.Errorf("GET /api/ping = (%d, %s), want (200, pong)", resp.StatusCode, body)
	}

	// ---- 3: TTL 续约 goroutine 周期 Renew ----
	waitF2(t, 2*time.Second, "续约 goroutine 至少完成两次 Renew", func() bool {
		return fake.renews.Load() >= 2
	})

	// ---- 4: Destroy → 反注册 + 续约停止 ----
	s.Destroy()
	if fake.deregisters.Load() != 1 {
		t.Errorf("Destroy 后 Deregister 次数 = %d, want 1", fake.deregisters.Load())
	}
	snapshot := fake.renews.Load()
	time.Sleep(80 * time.Millisecond) // > 3×interval
	if got := fake.renews.Load(); got != snapshot {
		t.Errorf("Destroy 后续约仍在跳: %d → %d", snapshot, got)
	}
}

// TestG2_HTTPConsul_HTTPCheckMode_CustomHealth HTTP check 模式 + 业务自检 +
// 自定义 health 路径：不起续约 goroutine；自检失败时探活返回 503。
func TestG2_HTTPConsul_HTTPCheckMode_CustomHealth(t *testing.T) {
	g1Setup(t)
	stubDiscoveryForHTTPConsul(t)
	created := injectFakeHTTPConsul(t)

	var healthy atomic.Bool
	healthy.Store(true)

	s := NewRPCServer()
	s.WithoutServiceClient()
	s.WithHTTPServiceConsul(web.Options{Addr: "127.0.0.1:0"}, HTTPRegistration{
		ServiceName:  "g2-web-httpcheck",
		UseHTTPCheck: true,
		HealthPath:   "/healthz",
		HealthCheck: func() error {
			if healthy.Load() {
				return nil
			}
			return errors.New("依赖故障")
		},
	})
	if s.Run() == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)

	if len(*created) != 1 {
		t.Fatalf("注册次数 = %d, want 1", len(*created))
	}
	fake := (*created)[0]
	if fake.opts.CheckMode != internal.HTTPCheckModeHTTP || fake.opts.HealthPath != "/healthz" {
		t.Errorf("opts = mode %q path %q, want http /healthz", fake.opts.CheckMode, fake.opts.HealthPath)
	}

	addr := s.httpServers[0].Addr()
	resp, _ := g1Get(t, addr, "/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("自检健康时 /healthz = %d, want 200", resp.StatusCode)
	}
	healthy.Store(false)
	resp, body := g1Get(t, addr, "/healthz", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("自检失败时 /healthz = (%d, %s), want 503(探活方据此摘流量)", resp.StatusCode, body)
	}

	// HTTP check 模式无续约 goroutine。
	time.Sleep(80 * time.Millisecond)
	if got := fake.renews.Load(); got != 0 {
		t.Errorf("HTTP check 模式不应续约, got %d 次", got)
	}
}

// TestG2_HTTPConsul_RegisterFailureTolerated 注册失败只告警不阻断启动——
// HTTP 服务照常监听（Consul 抖动不放大为发布失败），Destroy 不反注册（无句柄）。
func TestG2_HTTPConsul_RegisterFailureTolerated(t *testing.T) {
	g1Setup(t)
	stubDiscoveryForHTTPConsul(t)
	orig := newHTTPConsulServiceFn
	newHTTPConsulServiceFn = func(internal.HTTPServiceOptions) (httpConsulHandle, error) {
		return nil, errors.New("injected: consul unreachable")
	}
	t.Cleanup(func() { newHTTPConsulServiceFn = orig })

	s := NewRPCServer()
	s.WithoutServiceClient()
	s.WithHTTPServiceConsul(web.Options{Addr: "127.0.0.1:0"}, HTTPRegistration{ServiceName: "g2-web-fail"})
	if s.Run() == nil {
		t.Fatal("Run 应返回关闭通知通道(注册失败不得阻断启动)")
	}
	t.Cleanup(s.Destroy)

	if len(s.httpConsulServices) != 0 {
		t.Errorf("注册失败不应留下句柄, got %d", len(s.httpConsulServices))
	}
	resp, _ := g1Get(t, s.httpServers[0].Addr(), web.DefaultHealthPath, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("注册失败时 HTTP 服务应照常监听, /health = %d", resp.StatusCode)
	}
	s.Destroy() // 无句柄路径不得 panic
}

// TestG3_ClientOnly_HTTPConsulRegistered client-only 模式（runClientOnly）同样
// 走完整的 HTTP Consul 注册路径——纯 web 进程以 HTTP 形态进入服务发现。
func TestG3_ClientOnly_HTTPConsulRegistered(t *testing.T) {
	g1Setup(t)
	internal.ResetDiscoveryForTest()
	t.Cleanup(internal.ResetDiscoveryForTest)
	setEnvConfigForTest(t, "ConsulAddress", fmt.Sprintf("127.0.0.1:%d", freeF2Port(t))) // client 初始化允许失败
	created := injectFakeHTTPConsul(t)

	s := NewRPCServer()
	s.WithClientOnly()
	s.WithHTTPServiceConsul(web.Options{Addr: "127.0.0.1:0"}, HTTPRegistration{
		ServiceName: "g3-web",
		Interval:    20 * time.Millisecond,
	})
	if s.Run() == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)

	if len(*created) != 1 {
		t.Fatalf("client-only 模式注册次数 = %d, want 1", len(*created))
	}
	fake := (*created)[0]
	if want := s.httpServers[0].Addr(); fake.opts.Address != want {
		t.Errorf("注册地址 = %q, want %q", fake.opts.Address, want)
	}
	waitF2(t, 2*time.Second, "client-only 续约 goroutine 在跳", func() bool {
		return fake.renews.Load() >= 1
	})
	s.Destroy()
	if fake.deregisters.Load() != 1 {
		t.Errorf("Destroy 后 Deregister = %d, want 1", fake.deregisters.Load())
	}
}

// TestG2_ResolveHTTPAdvertiseAddr 公告地址解析：显式优先 / 通配 host 替换 /
// 自定义 ServiceAddress / 非法输入透传。
func TestG2_ResolveHTTPAdvertiseAddr(t *testing.T) {
	s := newBareServer()

	if got := s.resolveHTTPAdvertiseAddr("10.0.0.8:9000", "127.0.0.1:1234"); got != "10.0.0.8:9000" {
		t.Errorf("显式地址应原样返回, got %q", got)
	}
	if got := s.resolveHTTPAdvertiseAddr("", "127.0.0.1:1234"); got != "127.0.0.1:1234" {
		t.Errorf("具体 host 应保留, got %q", got)
	}

	// 通配 host + WithCustomServiceAddress → 配置 ServiceAddress。
	setEnvConfigForTest(t, "ServiceAddress", "10.1.2.3")
	s.customServiceAddress = true
	if got := s.resolveHTTPAdvertiseAddr("", "0.0.0.0:8090"); got != "10.1.2.3:8090" {
		t.Errorf("通配 host 应替换为配置 ServiceAddress, got %q", got)
	}
	if got := s.resolveHTTPAdvertiseAddr("", "[::]:8090"); got != "10.1.2.3:8090" {
		t.Errorf("IPv6 通配 host 应替换, got %q", got)
	}

	// 非法监听地址透传（防 panic）。
	if got := s.resolveHTTPAdvertiseAddr("", "not-an-addr"); got != "not-an-addr" {
		t.Errorf("非法地址应透传, got %q", got)
	}
}
