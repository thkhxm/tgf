package rpc

// E1 策略管道全覆盖的接线测试：
//   1. 网关主链路 doLogic→sendMessage 真实受限流/熔断管控（v3 审计：原先完全裸奔）
//   2. sendMessage 远程路径的超时保护（真实 rpcx server + 慢 handler）
//   3. BorderRPCMessage / BorderAllService* 的 nil 防御（原 xclient 判空缺失 → panic）
//   4. SendNoReplyRPCMessage 本地 fast path 的 panic 恢复 + 策略资源释放

import (
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cornelk/hashmap"
	rpcxclient "github.com/thkhxm/rpcx/v2/client"
	rpcxserver "github.com/thkhxm/rpcx/v2/server"
	"github.com/thkhxm/tgf"
	"golang.org/x/net/context"
)

// ---- 真实 rpcx server 测试设施 ----

// startTestRPCXServer 在 127.0.0.1 随机端口起一个真实 rpcx server 并注册 svc，
// 返回地址与关闭函数。用 ServeListener 消除"先探端口再监听"的竞态。
//
// 注意：本设施同时会建立真实 rpcx client 连接——race 模式下因 fork 的
// client.input() 已知竞态（F1 治理项）统一跳过，见 race_on_test.go。
func startTestRPCXServer(t *testing.T, moduleName string, svc interface{}) (addr string, shutdown func()) {
	t.Helper()
	if raceDetectorEnabled {
		t.Skip("rpcx fork client.input() 存在已知竞态(V3 审计 F1 fork 治理项)——race 模式跳过真实 rpcx client 链路,非 race 测试仍全量执行")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := rpcxserver.NewServer()
	if err := s.RegisterName(moduleName, svc, ""); err != nil {
		t.Fatalf("register %s: %v", moduleName, err)
	}
	go func() {
		_ = s.ServeListener("tcp", ln)
	}()
	return ln.Addr().String(), func() { _ = s.Close() }
}

// newP2PXClient 构造直连 addr 的 xclient（绕开 Consul）。
func newP2PXClient(t *testing.T, moduleName, addr string) rpcxclient.XClient {
	t.Helper()
	d, err := rpcxclient.NewPeer2PeerDiscovery("tcp@"+addr, "")
	if err != nil {
		t.Fatalf("p2p discovery: %v", err)
	}
	return rpcxclient.NewXClient(moduleName, rpcxclient.Failtry, rpcxclient.RandomSelect, d, rpcxclient.DefaultOption)
}

// injectRPCClient 把手工构造的 module→xclient 映射注入全局 rpcClient，
// 返回恢复函数。
func injectRPCClient(t *testing.T, moduleName string, xc rpcxclient.XClient) func() {
	t.Helper()
	orig := rpcClient
	nc := new(Client)
	nc.clients = hashmap.New[string, rpcxclient.XClient]()
	nc.whiteMethod = make([]string, 0)
	if xc != nil {
		nc.clients.Set(moduleName, xc)
	}
	rpcClient = nc
	return func() { rpcClient = orig }
}

// ---- 1. 网关主链路 sendMessage 接入策略管道 ----

// TestSendMessage_GatewayRateLimit 是 E1 的核心接线回归：
// 网关主链路（doLogic 调的 sendMessage）必须真实受 SetMethodPolicy 的限流管控。
// 通过单进程直通驱动：第 1 次放行（service 收到），第 2 次被令牌桶拒绝
//（service 不应再被调到）。
func TestSendMessage_GatewayRateLimit(t *testing.T) {
	svc, cleanup := withLocalEchoDispatch(t, "echo.Echo")
	defer cleanup()
	ClearMethodPolicies()
	defer ClearMethodPolicies()
	SetMethodPolicy("echo.Echo", MethodPolicy{RateLimit: 1})

	ct := newBareConnectData()
	args := &Args[*WSMessage]{ByteData: newEchoFrameData(t, []byte("rl"))}

	if err := sendMessage(ct, "echo", "Echo", args, &Reply[*WSMessage]{}); err != nil {
		t.Fatalf("第 1 次应放行, err=%v", err)
	}
	if svc.calls != 1 {
		t.Fatalf("service calls = %d, want 1", svc.calls)
	}

	err := sendMessage(ct, "echo", "Echo", args, &Reply[*WSMessage]{})
	if !errors.Is(err, ErrRPCRateLimited) {
		t.Fatalf("第 2 次应被限流, err=%v", err)
	}
	if svc.calls != 1 {
		t.Fatalf("被限流的请求不应抵达 service, calls=%d", svc.calls)
	}
}

// TestSendMessage_GatewayCircuitBreaker 验证网关主链路的熔断管控：
// 连续失败达到阈值后，后续请求被熔断器快速拒绝、不再抵达 service。
func TestSendMessage_GatewayCircuitBreaker(t *testing.T) {
	_, cleanup := withLocalEchoDispatch(t, "echo.Boom", "echo.Echo")
	defer cleanup()
	ClearMethodPolicies()
	defer ClearMethodPolicies()
	// Boom handler panic → localGateDispatch 转为 error → 计入熔断失败。
	SetMethodPolicy("echo.Boom", MethodPolicy{
		CircuitBreaker: CircuitBreakerConfig{FailureThreshold: 2, RecoveryInterval: time.Minute},
	})

	ct := newBareConnectData()
	args := &Args[*WSMessage]{ByteData: newEchoFrameData(t, nil)}

	for i := 0; i < 2; i++ {
		if err := sendMessage(ct, "echo", "Boom", args, &Reply[*WSMessage]{}); err == nil {
			t.Fatalf("Boom 应返回 panic 转换的 error")
		}
	}
	err := sendMessage(ct, "echo", "Boom", args, &Reply[*WSMessage]{})
	if !errors.Is(err, ErrRPCCircuitOpen) {
		t.Fatalf("达到失败阈值后网关链路应被熔断, err=%v", err)
	}
}

// TestSendMessage_PolicyRejectedNotCountedAsLoginFailure 验证执行顺序：
// 未登录 + 非白名单的本地拒绝发生在策略管道之前——不消耗限流配额。
func TestSendMessage_LoginCheckBeforePolicy(t *testing.T) {
	_, cleanup := withLocalEchoDispatch(t /* 无白名单 */)
	defer cleanup()
	ClearMethodPolicies()
	defer ClearMethodPolicies()
	SetMethodPolicy("echo.Echo", MethodPolicy{RateLimit: 1})

	ct := newBareConnectData() // 未登录
	args := &Args[*WSMessage]{ByteData: newEchoFrameData(t, nil)}

	// 未登录请求风暴：被登录检查拒绝，不应消耗令牌。
	for i := 0; i < 5; i++ {
		err := sendMessage(ct, "echo", "Echo", args, &Reply[*WSMessage]{})
		if err == nil || !strings.Contains(err.Error(), "用户未登录") {
			t.Fatalf("未登录应被拒, err=%v", err)
		}
	}

	// 登录后第一条请求仍应有令牌可用。
	ct2 := newBareConnectData()
	ct2.userId = "u-policy"
	if err := sendMessage(ct2, "echo", "Echo", args, &Reply[*WSMessage]{}); err != nil {
		t.Fatalf("登录后的首条请求应放行（令牌未被未登录风暴消耗）, err=%v", err)
	}
}

// ---- 2. 网关主链路远程路径的超时保护（真实 rpcx server）----

// slowRPCService 是真实 rpcx server 上的慢 handler。
type slowRPCService struct{}

func (s *slowRPCService) Slow(ctx context.Context, args *DefaultArgs, reply *DefaultReply) error {
	time.Sleep(800 * time.Millisecond)
	reply.C = 1
	return nil
}

// TestSendMessage_RemoteTimeout 验证 E1 的网关远程链路超时：
// 原实现是无 deadline 的同步 xclient.Call——慢后端会无限期阻塞连接的 logic
// goroutine。现在 per-method 超时 60ms 必须在远端 handler（800ms）完成前返回
// tgf.ErrorRPCTimeOut。
func TestSendMessage_RemoteTimeout(t *testing.T) {
	ResetLocalDispatcherForTest() // 确保不走本地直通
	defer ResetLocalDispatcherForTest()

	addr, shutdown := startTestRPCXServer(t, "slowmod", &slowRPCService{})
	defer shutdown()
	xc := newP2PXClient(t, "slowmod", addr)
	defer func() { _ = xc.Close() }()
	restore := injectRPCClient(t, "slowmod", xc)
	defer restore()

	SetMethodRPCTimeout("slowmod.Slow", 60*time.Millisecond)
	defer SetMethodRPCTimeout("slowmod.Slow", 0)

	ct := newBareConnectData()
	ct.userId = "u-timeout"

	start := time.Now()
	err := sendMessage(ct, "slowmod", "Slow", &DefaultArgs{C: "x"}, &DefaultReply{})
	elapsed := time.Since(start)

	if !errors.Is(err, tgf.ErrorRPCTimeOut) {
		t.Fatalf("慢后端应触发超时, err=%v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("超时应在 per-method 60ms 量级返回, 实际 %v", elapsed)
	}
}

// TestSendRPCMessageByStr_RemoteTimeout 验证 hook 链路（LoginHook/OfflineHook
// 走 SendRPCMessageByStr）同样受超时保护。
func TestSendRPCMessageByStr_RemoteTimeout(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()

	addr, shutdown := startTestRPCXServer(t, "slowhook", &slowRPCService{})
	defer shutdown()
	xc := newP2PXClient(t, "slowhook", addr)
	defer func() { _ = xc.Close() }()
	restore := injectRPCClient(t, "slowhook", xc)
	defer restore()

	SetMethodRPCTimeout("slowhook.Slow", 60*time.Millisecond)
	defer SetMethodRPCTimeout("slowhook.Slow", 0)

	start := time.Now()
	err := SendRPCMessageByStr(NewRPCContext(), "slowhook", "Slow", &DefaultArgs{C: "x"}, &DefaultReply{})
	if !errors.Is(err, tgf.ErrorRPCTimeOut) {
		t.Fatalf("SendRPCMessageByStr 应受超时保护, err=%v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("超时应快速返回, 实际 %v", elapsed)
	}
}

// ---- 3. Border* 的 nil 防御 ----

// TestBorderRPCMessage_NilClientNoPanic 回归：单进程/模块未上线时 xclient 为 nil，
// 原实现直接 xclient.Broadcast panic。
func TestBorderRPCMessage_NilClientNoPanic(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()
	restore := injectRPCClient(t, "ghost", nil) // clients 表为空
	defer restore()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("BorderRPCMessage 不应 panic: %v", r)
		}
	}()
	BorderRPCMessage(NewRPCContext(), ToUser.New(&ToUserReq{}, &ToUserRes{}))
}

// TestBorderAllService_NilClientNoPanic 回归：rpcClient 为 nil 时
// BorderAllServiceRPCMessageByContext(NotCheck) 原实现 rc.clients.Range panic。
func TestBorderAllService_NilClientNoPanic(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()
	orig := rpcClient
	rpcClient = nil
	defer func() { rpcClient = orig }()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("BorderAllService* 不应 panic: %v", r)
		}
	}()
	BorderAllServiceRPCMessageByContext(NewRPCContext(), ToUser.New(&ToUserReq{}, &ToUserRes{}))
	BorderAllServiceRPCMessageByContextNotCheck(NewRPCContext(), ToUser.New(&ToUserReq{}, &ToUserRes{}))
}

// ---- 4. SendNoReplyRPCMessage 本地 fast path：panic 恢复 + 策略释放 ----

// panicNoReplyService 的 Boom 方法直接 panic。
type panicNoReplyService struct {
	Module
	calls atomic.Int32
}

func (s *panicNoReplyService) Startup() (bool, error) { return true, nil }
func (s *panicNoReplyService) Boom(ctx context.Context, args *DefaultArgs, reply *DefaultReply) error {
	s.calls.Add(1)
	panic("no-reply boom")
}

// TestSendNoReplyRPCMessage_LocalPanicRecoveredAndReleased 验证两件事：
//  1. 本地 no-reply 路径 handler panic 不再崩进程（原裸 go func 无 recover）；
//  2. 策略并发信号量在 panic 后被释放（MaxConcurrency=1 时第二次调用仍能放行）。
func TestSendNoReplyRPCMessage_LocalPanicRecoveredAndReleased(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()
	svc := &panicNoReplyService{Module: Module{Name: "boomsvc", Version: "1.0"}}
	localDispatcher.Register("boomsvc", svc)
	localDispatchEnabled.Store(true)

	ClearMethodPolicies()
	defer ClearMethodPolicies()
	SetMethodPolicy("boomsvc.Boom", MethodPolicy{MaxConcurrency: 1})

	api := &ServiceAPI[*DefaultArgs, *DefaultReply]{ModuleName: "boomsvc", Name: "Boom"}

	if err := SendNoReplyRPCMessage(NewRPCContext(), api.New(&DefaultArgs{}, &DefaultReply{})); err != nil {
		t.Fatalf("no-reply 提交不应报错, err=%v", err)
	}
	// 等后台 goroutine 执行完（panic 被 recover + release 执行）。
	waitUntil(t, 2*time.Second, func() bool { return svc.calls.Load() >= 1 })

	// 并发额度应已释放：第二次提交仍能进入（若泄漏则 ErrRPCOverload）。
	waitUntil(t, 2*time.Second, func() bool {
		err := SendNoReplyRPCMessage(NewRPCContext(), api.New(&DefaultArgs{}, &DefaultReply{}))
		return err == nil
	})
	waitUntil(t, 2*time.Second, func() bool { return svc.calls.Load() >= 2 })
}

// waitUntil 轮询直到条件满足或超时。
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("条件在 %v 内未满足", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
