package rpc

// 单进程模式 / In-Process RPC 直通单测
//
// 覆盖：
//   1. Register / Lookup 基本行为
//   2. Call 正确路由到方法并填充 reply
//   3. Call 对不存在的 service / method / 错签名的方法都返回明确 error
//   4. ensureShareContext 不破坏已有 ctx
//   5. WithSingleProcess builder 同时置上两个 flag
//   6. registerLocalServices 在 flag 关闭时是 no-op
//   7. localDispatchEnabled 开关语义

import (
	"errors"
	"strings"
	"testing"

	"github.com/thkhxm/rpcx/share"
	"golang.org/x/net/context"
)

// dispatchTestService 是一个最小化的本地 service：满足 IService 核心方法
// 并额外提供几个 rpcx 约定签名的业务方法用于反射调用测试。
type dispatchTestService struct {
	Module

	lastCalledMethod string
	lastUserId       string
}

func (d *dispatchTestService) Startup() (bool, error) { return true, nil }

// dispatchReq / dispatchRes 是 Echo 方法的参数/返回类型。
type dispatchReq struct {
	Value string
}
type dispatchRes struct {
	Echo string
}

// Echo 是 rpcx 约定签名：(ctx, *Req, *Res) error
func (d *dispatchTestService) Echo(ctx context.Context, req *dispatchReq, reply *dispatchRes) error {
	d.lastCalledMethod = "Echo"
	if sc, ok := ctx.(*share.Context); ok {
		d.lastUserId = sc.GetReqMetaDataByKey("UserId")
	}
	_ = req
	_ = reply
	reply.Echo = "echo:" + req.Value
	return nil
}

// Fail 总是返回错误，用来测错误路径。
func (d *dispatchTestService) Fail(ctx context.Context, req *dispatchReq, reply *dispatchRes) error {
	return errors.New("boom")
}

// BadSignature 故意写错签名——返回值数量不对，用来验证签名校验。
func (d *dispatchTestService) BadSignature(ctx context.Context, req *dispatchReq, reply *dispatchRes) (string, error) {
	return "nope", nil
}

// ---- Register / Lookup ----

func TestDispatcher_RegisterAndLookup(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	svc := &dispatchTestService{Module: Module{Name: "demo", Version: "v1"}}
	localDispatcher.Register("demo", svc)

	v, ok := localDispatcher.Lookup("demo")
	if !ok {
		t.Fatal("注册后应能查到")
	}
	if v.IsNil() {
		t.Error("reflect.Value 不应为 nil")
	}
	if _, ok := localDispatcher.Lookup("missing"); ok {
		t.Error("未注册的 module 不应命中")
	}
}

func TestDispatcher_RegisterIgnoresEmpty(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	localDispatcher.Register("", &dispatchTestService{})
	localDispatcher.Register("x", nil)
	if _, ok := localDispatcher.Lookup(""); ok {
		t.Error("空 moduleName 注册应被忽略")
	}
}

// ---- Call ----

func TestDispatcher_Call_EchoSuccess(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	svc := &dispatchTestService{Module: Module{Name: "demo"}}
	localDispatcher.Register("demo", svc)

	req := &dispatchReq{Value: "hello"}
	reply := &dispatchRes{}

	sctx := share.NewContext(context.Background())
	sctx.SetReqMetaData("UserId", "u001")

	if err := localDispatcher.Call(sctx, "demo", "Echo", req, reply); err != nil {
		t.Fatalf("Call err: %v", err)
	}
	if reply.Echo != "echo:hello" {
		t.Errorf("reply 应为 echo:hello, 实际 %v", reply.Echo)
	}
	if svc.lastCalledMethod != "Echo" {
		t.Errorf("lastCalledMethod 应为 Echo, 实际 %v", svc.lastCalledMethod)
	}
	if svc.lastUserId != "u001" {
		t.Errorf("share.Context 的 UserId 应传递, 实际 %q", svc.lastUserId)
	}
}

func TestDispatcher_Call_MethodErrorPropagates(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	svc := &dispatchTestService{}
	localDispatcher.Register("demo", svc)

	err := localDispatcher.Call(context.Background(), "demo", "Fail", &dispatchReq{}, &dispatchRes{})
	if err == nil || err.Error() != "boom" {
		t.Errorf("应收到 boom 错误, 实际 %v", err)
	}
}

func TestDispatcher_Call_ServiceNotRegistered(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	err := localDispatcher.Call(context.Background(), "nope", "Echo", &dispatchReq{}, &dispatchRes{})
	if !errors.Is(err, ErrLocalServiceNotRegistered) {
		t.Errorf("期望 ErrLocalServiceNotRegistered, 实际 %v", err)
	}
}

func TestDispatcher_Call_MethodNotFound(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	localDispatcher.Register("demo", &dispatchTestService{})
	err := localDispatcher.Call(context.Background(), "demo", "NonExistent", &dispatchReq{}, &dispatchRes{})
	if !errors.Is(err, ErrLocalMethodNotFound) {
		t.Errorf("期望 ErrLocalMethodNotFound, 实际 %v", err)
	}
}

func TestDispatcher_Call_BadSignatureRejected(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	localDispatcher.Register("demo", &dispatchTestService{})
	err := localDispatcher.Call(context.Background(), "demo", "BadSignature", &dispatchReq{}, &dispatchRes{})
	if !errors.Is(err, ErrLocalMethodBadSignature) {
		t.Errorf("期望 ErrLocalMethodBadSignature, 实际 %v", err)
	}
}

func TestDispatcher_Call_NilCtxIsSafe(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	localDispatcher.Register("demo", &dispatchTestService{})
	err := localDispatcher.Call(nil, "demo", "Echo", &dispatchReq{Value: "x"}, &dispatchRes{})
	if err != nil {
		t.Errorf("nil ctx 应被 ensureShareContext 兜底, 实际 err %v", err)
	}
}

// ---- ensureShareContext ----

func TestEnsureShareContext_WrapsPlainContext(t *testing.T) {
	ctx := context.Background()
	upgraded := ensureShareContext(ctx)
	if _, ok := upgraded.(*share.Context); !ok {
		t.Error("plain ctx 应被包装成 share.Context")
	}
}

func TestEnsureShareContext_PreservesShareContext(t *testing.T) {
	orig := share.NewContext(context.Background())
	upgraded := ensureShareContext(orig)
	// orig 是 *share.Context，upgraded 是 context.Context；比较底层身份。
	if sc, ok := upgraded.(*share.Context); !ok || sc != orig {
		t.Error("已经是 share.Context 的应直接返回原值")
	}
}

func TestEnsureShareContext_NilSafe(t *testing.T) {
	upgraded := ensureShareContext(nil)
	if upgraded == nil {
		t.Fatal("nil ctx 应回落到一个非 nil share.Context")
	}
	if _, ok := upgraded.(*share.Context); !ok {
		t.Error("nil ctx 应被包装为 share.Context")
	}
}

// ---- Server builder ----

func TestWithInProcessDispatch_SetsFlag(t *testing.T) {
	s := newBareServer()
	s.WithInProcessDispatch()
	if !s.inProcessDispatch {
		t.Error("WithInProcessDispatch 应置上 flag")
	}
}

func TestWithSingleProcess_CombinesStandaloneAndDispatch(t *testing.T) {
	s := newBareServer()
	got := s.WithSingleProcess()
	if got != s {
		t.Error("WithSingleProcess 应返回 *Server 自己")
	}
	if !s.disableConsul {
		t.Error("WithSingleProcess 应 disableConsul")
	}
	if !s.disableClient {
		t.Error("WithSingleProcess 应 disableClient")
	}
	if !s.inProcessDispatch {
		t.Error("WithSingleProcess 应 inProcessDispatch")
	}
}

func TestRegisterLocalServices_NoopWhenFlagOff(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	s := newBareServer()
	s.service = append(s.service, &dispatchTestService{Module: Module{Name: "demo"}})
	s.registerLocalServices() // flag 未开启
	if localDispatchEnabled.Load() {
		t.Error("flag 关闭时不应启用 dispatch")
	}
	if _, ok := localDispatcher.Lookup("demo"); ok {
		t.Error("flag 关闭时不应注册到 dispatcher")
	}
}

func TestRegisterLocalServices_FlagOnRegistersAll(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	s := newBareServer()
	s.WithInProcessDispatch()
	s.service = append(s.service,
		&dispatchTestService{Module: Module{Name: "demo1"}},
		&dispatchTestService{Module: Module{Name: "demo2"}},
	)
	s.registerLocalServices()
	if !localDispatchEnabled.Load() {
		t.Error("flag 开启后应当 enable dispatch")
	}
	if _, ok := localDispatcher.Lookup("demo1"); !ok {
		t.Error("demo1 应被注册")
	}
	if _, ok := localDispatcher.Lookup("demo2"); !ok {
		t.Error("demo2 应被注册")
	}
}

// ---- 错误消息格式 ----

func TestDispatcher_ErrorMessagesIncludeContext(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	err := localDispatcher.Call(context.Background(), "mystery", "SomeMethod", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "mystery") {
		t.Errorf("错误消息应包含 module 名, 实际 %v", err)
	}

	localDispatcher.Register("demo", &dispatchTestService{})
	err = localDispatcher.Call(context.Background(), "demo", "Mystery", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Mystery") {
		t.Errorf("错误消息应包含 method 名, 实际 %v", err)
	}
}
