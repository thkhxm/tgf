package rpc

// A4 单测：buildPreServeHooks / buildPostServeHooks 的 pipeline 形状。
//
// 因为真实的默认 Hook（Consul + RPC Client）都依赖外部进程或全局 package 单例，
// 测试用一个"副作用计数"模式——不关心 Hook 具体做了什么，只看生成数量和执行顺序。

import (
	"testing"
)

// newBareServer 构造一个不做 NewRPCServer 副作用的最小 Server，
// 方便测试纯粹的 pipeline 组装逻辑。
func newBareServer() *Server {
	return &Server{
		beforeOptionals: make([]Optional, 0),
		afterOptionals:  make([]Optional, 0),
	}
}

func TestBuildPreServeHooks_DefaultIncludesConsul(t *testing.T) {
	s := newBareServer()
	// 用户没注册任何 Hook，只应得到 1 个默认 Consul Hook
	hooks := s.buildPreServeHooks()
	if len(hooks) != 1 {
		t.Errorf("default preServe hooks = %d, want 1 (Consul only)", len(hooks))
	}
}

func TestBuildPreServeHooks_WithoutConsulIsEmpty(t *testing.T) {
	s := newBareServer()
	s.WithoutConsul()
	hooks := s.buildPreServeHooks()
	if len(hooks) != 0 {
		t.Errorf("after WithoutConsul, preServe hooks = %d, want 0", len(hooks))
	}
}

func TestBuildPreServeHooks_UserHooksAppendAfterConsul(t *testing.T) {
	s := newBareServer()
	var order []string
	s.beforeOptionals = append(s.beforeOptionals, func(*Server) { order = append(order, "user1") })
	s.beforeOptionals = append(s.beforeOptionals, func(*Server) { order = append(order, "user2") })

	// 替换默认 Consul Hook 为"副作用可观察"版本:
	// 实现方式——不调 s.buildPreServeHooks 而是测"用户 Hook 追加在 Consul 之后"的语义。
	// 由于 Consul Hook 内部调 internal.UseConsulDiscovery()（全局副作用，不可观察），
	// 我们只能断言 len + 相对顺序：Consul 索引 0，用户 Hook 索引 1..n。
	hooks := s.buildPreServeHooks()
	if len(hooks) != 3 {
		t.Fatalf("hooks len = %d, want 3 (consul + 2 user)", len(hooks))
	}

	// 跑 hooks[1] 和 hooks[2]（跳过 hooks[0] Consul 因为会改全局状态）
	// 断言它们按用户注册顺序执行
	hooks[1](s)
	hooks[2](s)
	if len(order) != 2 || order[0] != "user1" || order[1] != "user2" {
		t.Errorf("user hooks order = %v, want [user1 user2]", order)
	}
}

func TestBuildPostServeHooks_DefaultIncludesClient(t *testing.T) {
	s := newBareServer()
	hooks := s.buildPostServeHooks()
	if len(hooks) != 1 {
		t.Errorf("default postServe hooks = %d, want 1 (Client only)", len(hooks))
	}
}

func TestBuildPostServeHooks_WithoutClientIsEmpty(t *testing.T) {
	s := newBareServer()
	s.WithoutServiceClient()
	hooks := s.buildPostServeHooks()
	if len(hooks) != 0 {
		t.Errorf("after WithoutServiceClient, postServe hooks = %d, want 0", len(hooks))
	}
}

func TestBuildPostServeHooks_UserHooksPrependBeforeClient(t *testing.T) {
	s := newBareServer()
	var order []string
	s.afterOptionals = append(s.afterOptionals, func(*Server) { order = append(order, "user1") })
	s.afterOptionals = append(s.afterOptionals, func(*Server) { order = append(order, "user2") })

	hooks := s.buildPostServeHooks()
	if len(hooks) != 3 {
		t.Fatalf("hooks len = %d, want 3 (2 user + client)", len(hooks))
	}

	// 跑前两个（用户 Hook），不跑 hooks[2] 因为它会启动真实 RPC client
	hooks[0](s)
	hooks[1](s)
	if len(order) != 2 || order[0] != "user1" || order[1] != "user2" {
		t.Errorf("user hooks order = %v, want [user1 user2]", order)
	}
}

func TestBuildPostServeHooks_BothFlagsCombined(t *testing.T) {
	s := newBareServer()
	s.WithoutConsul().WithoutServiceClient()

	if len(s.buildPreServeHooks()) != 0 {
		t.Errorf("WithoutConsul → preServe should be empty")
	}
	if len(s.buildPostServeHooks()) != 0 {
		t.Errorf("WithoutServiceClient → postServe should be empty")
	}
}

func TestNewRPCServer_DefaultFlagsEnableBoth(t *testing.T) {
	// 调 NewRPCServer 会触发 tgf.AddDestroyHandler 的 side effect，
	// 但不会启动任何真实连接/goroutine，可以单测。
	s := NewRPCServer()
	if s.disableConsul {
		t.Errorf("NewRPCServer should default-enable Consul (disableConsul=false)")
	}
	if s.disableClient {
		t.Errorf("NewRPCServer should default-enable Client (disableClient=false)")
	}
}

func TestWithoutFlagsChainable(t *testing.T) {
	// WithoutConsul / WithoutServiceClient 应返回 *Server 支持链式
	s := NewRPCServer().WithoutConsul().WithoutServiceClient()
	if !s.disableConsul || !s.disableClient {
		t.Errorf("chain returned Server with flags not set: consul=%v client=%v",
			s.disableConsul, s.disableClient)
	}
}
