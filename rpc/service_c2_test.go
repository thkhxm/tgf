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

	"github.com/smallnest/rpcx/client"
)

// stubService 是一个不嵌入 Module 的极简 IService 实现。
// 用来证明 "服务不是必须嵌入 Module"——只要实现 IService 核心方法就够。
type stubService struct {
	name string
}

func (s *stubService) GetName() string           { return s.name }
func (s *stubService) GetVersion() string        { return "v1" }
func (s *stubService) Startup() (bool, error)    { return true, nil }
func (s *stubService) Destroy(sub IService)      {}
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

func (l *lifecycleStub) AddUserLoginHook(h loginHook)   { l.loginHooks = append(l.loginHooks, h) }
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
