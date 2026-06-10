package rpc

// C2 单测：IService 重塑后的行为。
//
// 覆盖：
//   1. Module 基类的 LogicSyncMethods 默认 nil
//   2. 嵌入 Module 的服务自动满足 IStatefulService 子接口
//   3. 嵌入 Module 的服务自动满足 IUserLifecycleService 子接口
//   4. 自定义 service（不嵌入 Module）也可以按需实现子接口
//   5. 不满足子接口的 service 不会被误 cast

import (
	"context"
	"testing"

	"github.com/thkhxm/rpcx/v2/client"
)

// stubService 是一个不嵌入 Module 的极简 IService 实现。
// 用来证明 "服务不是必须嵌入 Module"——只要实现 IService 核心方法就够。
type stubService struct {
	name string
}

func (s *stubService) GetName() string            { return s.name }
func (s *stubService) GetVersion() string         { return "v1" }
func (s *stubService) Startup() (bool, error)     { return true, nil }
func (s *stubService) Destroy(sub IService)       {}
func (s *stubService) LogicSyncMethods() []string { return []string{"Sync1", "Sync2"} }

// statefulStub 在 stubService 基础上加 StateHandler，用来测 IStatefulService 断言。
type statefulStub struct {
	stubService
	stateCalls int
}

func (s *statefulStub) StateHandler(ctx context.Context, args *client.ConsulServerState, reply *string) error {
	s.stateCalls++
	return nil
}

// lifecycleStub 在 stubService 基础上加 AddUserLoginHook / AddUserOfflineHook。
type lifecycleStub struct {
	stubService
	loginHooks   []loginHook
	offlineHooks []offlineHook
}

func (l *lifecycleStub) AddUserLoginHook(h loginHook)     { l.loginHooks = append(l.loginHooks, h) }
func (l *lifecycleStub) AddUserOfflineHook(h offlineHook) { l.offlineHooks = append(l.offlineHooks, h) }

// ---- 测试用例 ----

func TestModule_LogicSyncMethodsDefaultNil(t *testing.T) {
	m := &Module{Name: "test", Version: "v1"}
	if m.LogicSyncMethods() != nil {
		t.Error("Module 默认 LogicSyncMethods 应为 nil")
	}
}

func TestModule_SatisfiesIService(t *testing.T) {
	// 嵌入 Module 的服务自然是 IService——但 Module 本身不是（缺 Startup）
	var svc IService = &moduleService{Module: Module{Name: "t", Version: "v1"}}
	_ = svc
}

// moduleService 是一个"嵌入 Module 且实现 Startup"的测试服务。
// 它自动满足 IService + IStatefulService + IUserLifecycleService 三个接口。
type moduleService struct {
	Module
}

func (ms *moduleService) Startup() (bool, error) { return true, nil }

func TestModuleService_SatisfiesIStatefulService(t *testing.T) {
	var svc IService = &moduleService{Module: Module{Name: "t", Version: "v1"}}
	if _, ok := svc.(IStatefulService); !ok {
		t.Error("嵌入 Module 的服务应当自动满足 IStatefulService")
	}
}

func TestModuleService_SatisfiesIUserLifecycleService(t *testing.T) {
	var svc IService = &moduleService{Module: Module{Name: "t", Version: "v1"}}
	if _, ok := svc.(IUserLifecycleService); !ok {
		t.Error("嵌入 Module 的服务应当自动满足 IUserLifecycleService")
	}
}

func TestStubService_DoesNotSatisfySubInterfaces(t *testing.T) {
	var svc IService = &stubService{name: "stub"}
	if _, ok := svc.(IStatefulService); ok {
		t.Error("stubService 不应满足 IStatefulService")
	}
	if _, ok := svc.(IUserLifecycleService); ok {
		t.Error("stubService 不应满足 IUserLifecycleService")
	}
}

func TestStatefulStub_SatisfiesIStatefulService(t *testing.T) {
	var svc IService = &statefulStub{}
	stateful, ok := svc.(IStatefulService)
	if !ok {
		t.Fatal("statefulStub 应满足 IStatefulService")
	}
	if err := stateful.StateHandler(context.Background(), nil, nil); err != nil {
		t.Errorf("StateHandler err: %v", err)
	}
}

func TestLifecycleStub_SatisfiesIUserLifecycleService(t *testing.T) {
	var svc IService = &lifecycleStub{}
	lc, ok := svc.(IUserLifecycleService)
	if !ok {
		t.Fatal("lifecycleStub 应满足 IUserLifecycleService")
	}
	// 调用两个方法
	lc.AddUserLoginHook(func(context.Context, string) error { return nil })
	lc.AddUserOfflineHook(func(context.Context, string, bool) error { return nil })
}

func TestStubService_LogicSyncMethodsReturnsList(t *testing.T) {
	var svc IService = &stubService{name: "stub"}
	m := svc.LogicSyncMethods()
	if len(m) != 2 || m[0] != "Sync1" {
		t.Errorf("LogicSyncMethods 返回错 %v", m)
	}
}

// ---- C2 子接口真实消费方测试（CLEAN / E6）----
//
// 证明 wireServiceCapabilities 真的对 service 做了 IStatefulService /
// IUserLifecycleService 接口断言，并据此扇出全局钩子 + 盘点 state 能力。
// 这把 C2 两个子接口从"定义了没人用的死代码"变成有真实消费点的能力。

// countingLifecycle 是嵌入 Module 的 service，额外记录被扇入的钩子数量，
// 用来断言全局钩子真的通过 IUserLifecycleService 断言注册了进来。
type countingLifecycle struct {
	Module
}

func (c *countingLifecycle) Startup() (bool, error) { return true, nil }

func TestWireServiceCapabilities_FansOutGlobalHooks(t *testing.T) {
	resetGlobalHooksForTest()
	t.Cleanup(resetGlobalHooksForTest)

	var loginCalls, offlineCalls int
	RegisterGlobalLoginHook(func(context.Context, string) error { loginCalls++; return nil })
	RegisterGlobalLoginHook(func(context.Context, string) error { loginCalls++; return nil })
	RegisterGlobalOfflineHook(func(context.Context, string, bool) error { offlineCalls++; return nil })

	svcA := &countingLifecycle{Module: Module{Name: "svcA", Version: "v1"}}
	svcB := &countingLifecycle{Module: Module{Name: "svcB", Version: "v1"}}
	// plain：裸实现 IService、不满足任何子接口。
	plain := &stubService{name: "plain"}

	report := wireServiceCapabilities([]IService{svcA, svcB, plain})

	// 两个 Module 服务应同时满足 stateful + lifecycle 子接口。
	if len(report.Lifecycle) != 2 {
		t.Fatalf("Lifecycle 命中数应为 2, got %d (%v)", len(report.Lifecycle), report.Lifecycle)
	}
	if len(report.Stateful) != 2 {
		t.Fatalf("Stateful 命中数应为 2, got %d (%v)", len(report.Stateful), report.Stateful)
	}
	// plain 不满足任何子接口。
	if len(report.PlainOnly) != 1 || report.PlainOnly[0] != "plain" {
		t.Fatalf("PlainOnly 应为 [plain], got %v", report.PlainOnly)
	}

	// 2 个全局登录钩子 × 2 个 lifecycle 服务 = 4 次扇出；下线钩子 1×2=2。
	if report.LoginHooksFanned != 4 {
		t.Errorf("LoginHooksFanned 应为 4, got %d", report.LoginHooksFanned)
	}
	if report.OfflineHooksFanned != 2 {
		t.Errorf("OfflineHooksFanned 应为 2, got %d", report.OfflineHooksFanned)
	}

	// 真正触发钩子：每个服务的 LoginHook/OfflineHook 应能调到扇入的全局钩子。
	for _, svc := range []*countingLifecycle{svcA, svcB} {
		if err := svc.LoginHook(context.Background(), &DefaultArgs{C: "u1"}, &EmptyReply{}); err != nil {
			t.Fatalf("LoginHook err: %v", err)
		}
		if err := svc.OfflineHook(context.Background(), &OfflineReq{UserId: "u1"}, &EmptyReply{}); err != nil {
			t.Fatalf("OfflineHook err: %v", err)
		}
	}
	// 2 服务 × 2 登录钩子 = 4 次登录回调；2 服务 × 1 下线钩子 = 2 次下线回调。
	if loginCalls != 4 {
		t.Errorf("全局登录钩子应被回调 4 次, got %d", loginCalls)
	}
	if offlineCalls != 2 {
		t.Errorf("全局下线钩子应被回调 2 次, got %d", offlineCalls)
	}
}

func TestWireServiceCapabilities_StatefulOnly(t *testing.T) {
	resetGlobalHooksForTest()
	t.Cleanup(resetGlobalHooksForTest)

	// statefulStub 只实现 StateHandler（IStatefulService），不实现 lifecycle。
	report := wireServiceCapabilities([]IService{&statefulStub{}})
	if len(report.Stateful) != 1 {
		t.Fatalf("Stateful 应命中 1, got %v", report.Stateful)
	}
	if len(report.Lifecycle) != 0 {
		t.Fatalf("Lifecycle 应命中 0, got %v", report.Lifecycle)
	}
	// 满足 stateful 即 matched，不进 PlainOnly。
	if len(report.PlainOnly) != 0 {
		t.Fatalf("PlainOnly 应为空, got %v", report.PlainOnly)
	}
}

func TestRegisterLocalServices_PublishesCapabilityReport(t *testing.T) {
	resetGlobalHooksForTest()
	t.Cleanup(resetGlobalHooksForTest)
	t.Cleanup(func() { setLastServiceCapabilities(ServiceCapabilityReport{}) })

	RegisterGlobalLoginHook(func(context.Context, string) error { return nil })

	// 构造一个 Server，装载一个 Module 服务 + 一个裸服务，跑 registerLocalServices
	// （Server.Run 的真实调用点），断言盘点结果被发布到 LastServiceCapabilities。
	s := &Server{}
	s.service = []IService{
		&countingLifecycle{Module: Module{Name: "lc", Version: "v1"}},
		&stubService{name: "bare"},
	}
	s.registerLocalServices()

	rep := LastServiceCapabilities()
	if len(rep.Lifecycle) != 1 || rep.Lifecycle[0] != "lc" {
		t.Fatalf("LastServiceCapabilities.Lifecycle 应为 [lc], got %v", rep.Lifecycle)
	}
	if len(rep.PlainOnly) != 1 || rep.PlainOnly[0] != "bare" {
		t.Fatalf("LastServiceCapabilities.PlainOnly 应为 [bare], got %v", rep.PlainOnly)
	}
	if rep.LoginHooksFanned != 1 {
		t.Errorf("应扇出 1 个全局登录钩子, got %d", rep.LoginHooksFanned)
	}
}
