package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F2 单测：注册时序（Serve 就绪后注册 / Startup 失败不注册）
//             与 Consul TTL health 心跳接线（挂载→续约→失败降级→反注册）。
//
// 真实 Consul 的端到端行为（KV 可见即可连、kill 进程 TTL 内摘除）由
// it_f2_register_order_test.go / internal/it_health_test.go 的 integration
// 用例覆盖；本文件用注入桩在无 Consul 的干净环境下确定性锁住时序契约：
//   - 桩 discovery（internal.SetDiscoveryForTest）让 Run 走完整的"有发现"分支；
//   - 桩注册插件在 Register 回调内当场拨号验证"注册瞬间监听已就绪"；
//   - 桩 TTL renewer 录制 Renew/Deregister，验证心跳与停机接线。
//
//2026/6/10
//***************************************************

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	client2 "github.com/thkhxm/rpcx-consul/v2/client"
	"github.com/thkhxm/rpcx/v2/server"
	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/db"
	"github.com/thkhxm/tgf/v2/rpc/internal"
)

// f2RecorderPlugin 是 ConsulRegisterPlugin 的录制桩：
// Register 回调发生在 rpcx RegisterName 内部——正是节点"发布到 Consul"的时刻，
// 在这里当场拨号 + 读取 Startup 完成标志，把时序断言钉在事件发生点上。
type f2RecorderPlugin struct {
	mu                    sync.Mutex
	addr                  string
	startupDone           *atomic.Bool
	registered            []string
	unregistered          []string
	stopped               atomic.Bool
	dialOKAtRegister      map[string]bool
	startupDoneAtRegister map[string]bool
}

func newF2RecorderPlugin(startupDone *atomic.Bool) *f2RecorderPlugin {
	return &f2RecorderPlugin{
		startupDone:           startupDone,
		dialOKAtRegister:      make(map[string]bool),
		startupDoneAtRegister: make(map[string]bool),
	}
}

func (p *f2RecorderPlugin) Register(name string, _ interface{}, _ string) error {
	// 注册瞬间拨号：F2 契约要求此刻监听必须已就绪（Serve ready → 才注册）。
	conn, err := net.DialTimeout("tcp", p.addr, 2*time.Second)
	if conn != nil {
		_ = conn.Close()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.registered = append(p.registered, name)
	p.dialOKAtRegister[name] = err == nil
	p.startupDoneAtRegister[name] = p.startupDone.Load()
	return nil
}

func (p *f2RecorderPlugin) Unregister(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unregistered = append(p.unregistered, name)
	return nil
}

func (p *f2RecorderPlugin) Stop() error {
	p.stopped.Store(true)
	return nil
}

func (p *f2RecorderPlugin) snapshotRegistered() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.registered...)
}

// f2StubDiscovery 实现 internal.IRPCDiscovery，把录制插件接进 Run 的发现分支。
type f2StubDiscovery struct{ plugin *f2RecorderPlugin }

func (d *f2StubDiscovery) RegisterServer(ip string) server.Plugin {
	d.plugin.addr = ip
	return d.plugin
}
func (d *f2StubDiscovery) RegisterDiscovery(string) *client2.ConsulDiscovery { return nil }
func (d *f2StubDiscovery) GetDiscovery(string) *client2.ConsulDiscovery     { return nil }

// f2FakeRenewer 录制 TTL 续约/反注册调用，并支持注入续约失败。
type f2FakeRenewer struct {
	renews      atomic.Int64
	deregisters atomic.Int64
	failRenew   atomic.Bool
}

func (r *f2FakeRenewer) Renew(string) error {
	r.renews.Add(1)
	if r.failRenew.Load() {
		return errors.New("injected renew failure")
	}
	return nil
}
func (r *f2FakeRenewer) Deregister() error {
	r.deregisters.Add(1)
	return nil
}

// f2OkService 正常启动的最小服务；Startup 完成时打点（供注册时序断言）。
type f2OkService struct {
	Module
	startupDone *atomic.Bool
}

func (s *f2OkService) GetName() string    { return "f2-ok" }
func (s *f2OkService) GetVersion() string { return "1.0" }
func (s *f2OkService) Startup() (bool, error) {
	// 模拟真实 Startup 耗时，放大"先注册后 Startup"旧时序下必然暴露的窗口。
	time.Sleep(50 * time.Millisecond)
	s.startupDone.Store(true)
	return true, nil
}

// f2FailService Startup 必然失败的服务——F2 契约：绝不能出现在注册名单里。
type f2FailService struct {
	Module
}

func (s *f2FailService) GetName() string        { return "f2-fail" }
func (s *f2FailService) GetVersion() string     { return "1.0" }
func (s *f2FailService) Startup() (bool, error) { return false, errors.New("injected startup failure") }

// TestF2_RunOrder_ServeReadyBeforeRegister_StartupFailNotRegistered 是 F2 注册
// 时序的核心回归（消费方：Server.Run 步骤 1~5 + 心跳 goroutine + Destroy）：
//  1. 注册回调瞬间监听必须可拨通（Serve 就绪后才注册）；
//  2. 注册回调瞬间 Startup 必须已完成（Startup 先行）;
//  3. Startup 失败的服务绝不进入注册名单（无残留，比反注册更强）；
//  4. 注册成功后 TTL health 挂载，心跳周期续约；
//  5. Destroy：TTL health 反注册 + 注册插件 Stop。
func TestF2_RunOrder_ServeReadyBeforeRegister_StartupFailNotRegistered(t *testing.T) {
	// Run 会触发 db.Run——关闭缓存层避免单测依赖 Redis/MySQL；结束恢复默认值。
	db.WithCacheModule(tgf.CacheModuleClose)
	t.Cleanup(func() { db.WithCacheModule(tgf.CacheModuleRedis) })

	var startupDone atomic.Bool
	plugin := newF2RecorderPlugin(&startupDone)
	internal.SetDiscoveryForTest(&f2StubDiscovery{plugin: plugin})
	t.Cleanup(internal.ResetDiscoveryForTest)

	renewer := &f2FakeRenewer{}
	var healthOpts internal.TTLHealthOptions
	origNewTTL := newTTLHealthFn
	newTTLHealthFn = func(opt internal.TTLHealthOptions) (ttlHealthRenewer, error) {
		healthOpts = opt
		return renewer, nil
	}
	t.Cleanup(func() { newTTLHealthFn = origNewTTL })

	// 申请确定空闲的端口（WithRandomServicePort 撞上占用端口会 os.Exit(1) 杀掉
	// 整个测试进程，不可用于单测）。
	setEnvConfigForTest(t, "ServicePort", fmt.Sprintf("%d", freeF2Port(t)))

	s := NewRPCServer()
	s.WithoutServiceClient()
	s.WithHealthCheck(20 * time.Millisecond)
	s.WithService(&f2OkService{startupDone: &startupDone})
	s.WithService(&f2FailService{})

	done := s.Run()
	if done == nil {
		t.Fatal("Run 应返回关闭通知通道")
	}
	t.Cleanup(s.Destroy)

	// ---- 1+2+3: 注册名单与时序 ----
	registered := plugin.snapshotRegistered()
	if len(registered) == 0 {
		t.Fatal("Run 返回后应已完成注册(Run 内同步执行)")
	}
	hasOK := false
	for _, name := range registered {
		if name == "f2-fail" {
			t.Error("Startup 失败的服务 f2-fail 不应被注册(F2: 失败者绝不发布)")
		}
		if name == "f2-ok" {
			hasOK = true
		}
		plugin.mu.Lock()
		dialOK := plugin.dialOKAtRegister[name]
		startedOK := plugin.startupDoneAtRegister[name]
		plugin.mu.Unlock()
		if !dialOK {
			t.Errorf("服务 %v 注册瞬间监听未就绪(addr=%v)——违反 Serve-ready-before-register", name, plugin.addr)
		}
		if !startedOK {
			t.Errorf("服务 %v 注册瞬间 Startup 尚未完成——违反 Startup 先行", name)
		}
	}
	if !hasOK {
		t.Errorf("f2-ok 应在注册名单中, got %v", registered)
	}

	// ---- 4: TTL health 挂载 + 心跳续约 ----
	if healthOpts.ServiceAddress != plugin.addr {
		t.Errorf("TTL health 注册地址 = %v, want %v", healthOpts.ServiceAddress, plugin.addr)
	}
	foundModule := false
	for _, m := range healthOpts.Modules {
		if m == "f2-fail" {
			t.Error("TTL health 的 Modules 不应包含 Startup 失败的服务")
		}
		if m == "f2-ok" {
			foundModule = true
		}
	}
	if !foundModule {
		t.Errorf("TTL health 的 Modules 应包含 f2-ok, got %v", healthOpts.Modules)
	}
	waitF2(t, 2*time.Second, "心跳至少完成一次 TTL 续约", func() bool {
		return renewer.renews.Load() >= 1
	})
	if !s.IsHealthy() {
		t.Error("续约成功期间 IsHealthy 应为 true")
	}

	// 续约失败 → unhealthy；恢复 → healthy（心跳降级/自愈语义）。
	renewer.failRenew.Store(true)
	waitF2(t, 2*time.Second, "续约失败后转 unhealthy", func() bool { return !s.IsHealthy() })
	renewer.failRenew.Store(false)
	waitF2(t, 2*time.Second, "续约恢复后转回 healthy", func() bool { return s.IsHealthy() })

	// ---- 5: Destroy 反注册 ----
	s.Destroy()
	if renewer.deregisters.Load() < 1 {
		t.Error("Destroy 应触发 TTL health Deregister")
	}
	if !plugin.stopped.Load() {
		t.Error("Destroy 应触发注册插件 Stop(KV 反注册)")
	}
}

// TestF2_SetupConsulTTLHealth_FailureTolerated 验证 TTL health 注册失败不阻断
// 启动（KV 注册有 session TTL 兜底，agent check 只是可观测层）。
func TestF2_SetupConsulTTLHealth_FailureTolerated(t *testing.T) {
	origNewTTL := newTTLHealthFn
	newTTLHealthFn = func(internal.TTLHealthOptions) (ttlHealthRenewer, error) {
		return nil, errors.New("injected: consul unreachable")
	}
	t.Cleanup(func() { newTTLHealthFn = origNewTTL })

	s := newBareServer()
	s.setupConsulTTLHealth("127.0.0.1:18000", []string{"m"})
	if s.consulHealth != nil {
		t.Error("注册失败时 consulHealth 必须保持 nil(不得 typed-nil 进接口)")
	}
	// 默认 interval 已被补齐——心跳仍会启动（维持 A6 日志行为）。
	if s.healthInterval != internal.DefaultTTLHealthInterval {
		t.Errorf("healthInterval = %v, want 默认 %v", s.healthInterval, internal.DefaultTTLHealthInterval)
	}
}

// TestF2_UnregisterFromConsul_HealthOnly 验证只有 TTL health（无注册插件）时
// 停机路径也能完成反注册且不卡死——覆盖 unregisterFromConsul 的 !spOK 分支。
func TestF2_UnregisterFromConsul_HealthOnly(t *testing.T) {
	s := newBareServer()
	r := &f2FakeRenewer{}
	s.consulHealth = r
	finished := make(chan struct{})
	go func() {
		s.unregisterFromConsul()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("unregisterFromConsul 不应阻塞")
	}
	if r.deregisters.Load() != 1 {
		t.Errorf("Deregister 调用次数 = %d, want 1", r.deregisters.Load())
	}
}

// freeF2Port 申请一个空闲 TCP 端口。
func freeF2Port(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请空闲端口失败: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitF2 轮询断言（带超时 Fatal）。
func waitF2(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时(%v): %s", timeout, what)
}
