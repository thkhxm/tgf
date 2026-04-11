package rpc

// C1 单测：WithStandalone + WithGatewayOptions 的行为。
//
// 这些用例不起真实 listener，只验证 builder pipeline 生成的状态——和 A4 的
// rpcserver_hooks_test.go 思路一致：用一个"不跑 NewRPCServer 的裸 Server"，
// 断言 flag / hook 数量 / 追加顺序。

import (
	"testing"
)

func TestWithStandalone_SetsBothDisableFlags(t *testing.T) {
	s := newBareServer()
	s.WithStandalone()
	if !s.disableConsul {
		t.Error("WithStandalone 应当设置 disableConsul")
	}
	if !s.disableClient {
		t.Error("WithStandalone 应当设置 disableClient")
	}
}

func TestWithStandalone_IsChainable(t *testing.T) {
	s := newBareServer()
	got := s.WithStandalone()
	if got != s {
		t.Error("WithStandalone 应返回 *Server 自己，支持链式调用")
	}
}

func TestWithStandalone_NoPreServeHooksFromConsul(t *testing.T) {
	s := newBareServer()
	s.WithStandalone()
	hooks := s.buildPreServeHooks()
	if len(hooks) != 0 {
		t.Errorf("standalone 模式下 preServe hooks 应为 0, 实际 %d", len(hooks))
	}
	posts := s.buildPostServeHooks()
	if len(posts) != 0 {
		t.Errorf("standalone 模式下 postServe hooks 应为 0, 实际 %d", len(posts))
	}
}

func TestWithGatewayOptions_AppendsPreServeHook(t *testing.T) {
	s := newBareServer()
	s.WithGatewayOptions(GatewayOptions{TCPPort: "18080"})
	if len(s.beforeOptionals) != 1 {
		t.Errorf("期望 1 个 preServe hook, 实际 %d", len(s.beforeOptionals))
	}
}

// TestWithGatewayOptions_RegistersGateService 通过执行 Hook 断言它确实注册了 GateService。
func TestWithGatewayOptions_RegistersGateService(t *testing.T) {
	s := newBareServer()
	s.WithGatewayOptions(GatewayOptions{TCPPort: "18081"})

	// 直接执行 Hook——Hook 内部只改 s.service，不会起真实 listener
	for _, h := range s.beforeOptionals {
		h(s)
	}
	if len(s.service) != 1 {
		t.Fatalf("期望注册 1 个 service, 实际 %d", len(s.service))
	}
	gs, ok := s.service[0].(*GateService)
	if !ok {
		t.Fatalf("期望 *GateService, 实际 %T", s.service[0])
	}
	if gs.kcpBuilder != nil {
		t.Error("无 KCP option 时 kcpBuilder 应当为 nil")
	}
}

func TestWithGatewayOptions_WithKCP(t *testing.T) {
	kcp := NewKCPBuilder("19999")
	s := newBareServer()
	s.WithGatewayOptions(GatewayOptions{
		TCPPort: "18082",
		KCP:     kcp,
	})
	for _, h := range s.beforeOptionals {
		h(s)
	}
	if len(s.service) != 1 {
		t.Fatalf("期望 1 个 service, 实际 %d", len(s.service))
	}
	gs := s.service[0].(*GateService)
	if gs.kcpBuilder == nil {
		t.Error("传入 KCP option 后 GateService.kcpBuilder 应当非 nil")
	}
}

// TestWithGatewayOptions_DeprecatedAliases 验证老 API 薄包装能转到新 API。
func TestWithGatewayOptions_DeprecatedAliases(t *testing.T) {
	// WithGateway
	s1 := newBareServer()
	s1.WithGateway("18083")
	if len(s1.beforeOptionals) != 1 {
		t.Error("WithGateway 应注册 1 个 hook")
	}

	// WithGatewayWS
	s2 := newBareServer()
	s2.WithGatewayWS("18084", "/ws")
	if len(s2.beforeOptionals) != 1 {
		t.Error("WithGatewayWS 应注册 1 个 hook")
	}

	// WithGatewayWSS
	s3 := newBareServer()
	s3.WithGatewayWSS("18085", "/wss", "key.pem", "cert.pem")
	if len(s3.beforeOptionals) != 1 {
		t.Error("WithGatewayWSS 应注册 1 个 hook")
	}
}

// TestWithGatewayOptions_CompositeWithStandalone 验证 standalone + gateway 的组合。
func TestWithGatewayOptions_CompositeWithStandalone(t *testing.T) {
	s := newBareServer()
	s.WithStandalone().WithGatewayOptions(GatewayOptions{TCPPort: "18086", WSPath: "/ws"})

	pre := s.buildPreServeHooks()
	// standalone → Consul Hook 跳过；只剩用户的 gateway hook
	if len(pre) != 1 {
		t.Errorf("期望 1 个 preServe hook（只有 gateway）, 实际 %d", len(pre))
	}
	post := s.buildPostServeHooks()
	if len(post) != 0 {
		t.Errorf("期望 0 个 postServe hook, 实际 %d", len(post))
	}
}
