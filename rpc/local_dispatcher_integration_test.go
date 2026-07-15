package rpc

// 单进程模式集成测试：验证 SendRPCMessage 的 fast-path 真的路由到 localDispatcher。
// 这个文件的测试都不起真实 rpcx listener——完全靠 localDispatchEnabled 开关让
// SendRPCMessage 走本地分支。

import (
	"errors"
	"testing"

	"context"

	"github.com/thkhxm/rpcx/v2/share"
)

// ---- 测试 service ----

type integService struct {
	Module
	echoCalls int
}

func (s *integService) Startup() (bool, error) { return true, nil }

type integReq struct {
	Payload string
}
type integRes struct {
	Result string
}

func (s *integService) Greet(ctx context.Context, req *integReq, reply *integRes) error {
	s.echoCalls++
	reply.Result = "hi:" + req.Payload
	return nil
}

func (s *integService) AlwaysFail(ctx context.Context, req *integReq, reply *integRes) error {
	return errors.New("deliberate-fail")
}

// 构造一个 ServiceAPI 模拟业务调用点
func newIntegAPI(module, name string, req *integReq) *ServiceAPI[*integReq, *integRes] {
	return &ServiceAPI[*integReq, *integRes]{
		ModuleName:  module,
		Name:        name,
		MessageType: module + "." + name,
		args:        req,
		reply:       &integRes{},
	}
}

func TestSendRPCMessage_InProcessFastPathSuccess(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()
	ClearMethodPolicies() // 确保策略管道不干扰

	svc := &integService{Module: Module{Name: "hello"}}
	localDispatcher.Register("hello", svc)
	localDispatchEnabled.Store(true)

	api := newIntegAPI("hello", "Greet", &integReq{Payload: "world"})
	reply, err := SendRPCMessage(share.NewContext(context.Background()), api)
	if err != nil {
		t.Fatalf("SendRPCMessage err: %v", err)
	}
	if reply == nil || reply.Result != "hi:world" {
		t.Errorf("reply 错, 实际 %+v", reply)
	}
	if svc.echoCalls != 1 {
		t.Errorf("echoCalls 应为 1, 实际 %d", svc.echoCalls)
	}
}

func TestSendRPCMessage_InProcessFastPathPropagatesError(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()
	ClearMethodPolicies()

	localDispatcher.Register("hello", &integService{})
	localDispatchEnabled.Store(true)

	api := newIntegAPI("hello", "AlwaysFail", &integReq{})
	_, err := SendRPCMessage(share.NewContext(context.Background()), api)
	if err == nil || err.Error() != "deliberate-fail" {
		t.Errorf("应收到 deliberate-fail, 实际 %v", err)
	}
}

// 注：SendRPCMessage 的 "dispatch disabled fall back to rpcx" / "module missing
// fall back to rpcx" 两种负向路径的测试被刻意省略——它们需要一个真实的 rpcx
// client 环境才能执行到退出点，单元测试里搭 rpcx 客户端成本高、稳定性差。
// 这两条路径的 fast-path 判断逻辑由 TestDispatcher_* 和 TestSendRPCMessage_InProcess*
// 两组合起来已经充分覆盖。

func TestSendRPCMessage_PolicyAppliesBeforeLocalDispatch(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()
	ClearMethodPolicies()
	defer ClearMethodPolicies()

	svc := &integService{}
	localDispatcher.Register("hello", svc)
	localDispatchEnabled.Store(true)

	// 配置 MaxConcurrency=0（刻意触发）不能用，因为 0 表示不限。用 Timeout
	// 也不太容易触发——改用直接设置一个断路器并手动把它打开。
	// 这里用一个完整的三连击断路器配置来验证策略在本地调用路径也生效。
	SetMethodPolicy("hello.Greet", MethodPolicy{
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold: 1,
			RecoveryInterval: 10 * 1000 * 1000 * 1000, // 10s
		},
	})
	mr := resolveMethodPolicy("hello", "Greet")
	// 手动触发一次失败让断路器 Open
	r, err := mr.beforeCall()
	if err != nil {
		t.Fatalf("首次 beforeCall 应通过, 实际 %v", err)
	}
	r(errors.New("force-fail")) // markFailure → Open

	// 现在 SendRPCMessage 应当被策略拦截，不走 local dispatcher
	api := newIntegAPI("hello", "Greet", &integReq{Payload: "x"})
	_, err = SendRPCMessage(share.NewContext(context.Background()), api)
	if !errors.Is(err, ErrRPCCircuitOpen) {
		t.Errorf("期望 ErrRPCCircuitOpen, 实际 %v", err)
	}
	if svc.echoCalls != 0 {
		t.Error("断路器打开时 local service 不应被调用")
	}
}
