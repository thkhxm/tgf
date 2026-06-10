package rpc

// D4 / P0-2 测试：单进程模式下的网关本地直通 + 停机钩子。
//
// 覆盖：
//   1. sendMessage 本地直通分支（白名单/登录态语义、模块缺失时明确报错不 panic）
//   2. localGateDispatch 的类型桥接（Args/Reply 泛型实例化差异、panic 恢复、签名校验）
//   3. 端到端：真实 TCP listener + 客户端帧 → handleConn → doLogic → 本地 service →
//      响应帧回到客户端（v3 审计：该路径原先首条消息即 nil-discovery panic）
//   4. CloseListeners 停机钩子：停止 accept、已建立连接不受影响、幂等

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// newGateArgsForTest / newGateReplyForTest 构造 doLogic 真实使用的网关泛型形参。
func newGateArgsForTest(raw []byte) *Args[protoreflect.ProtoMessage] {
	return &Args[protoreflect.ProtoMessage]{ByteData: raw}
}

func newGateReplyForTest() *Reply[protoreflect.ProtoMessage] {
	return &Reply[protoreflect.ProtoMessage]{}
}

func getReplyCodeForTest(r *Reply[protoreflect.ProtoMessage]) int32   { return r.Code }
func getReplyBytesForTest(r *Reply[protoreflect.ProtoMessage]) []byte { return r.ByteData }

// ---- 测试用本地 service ----

// spEchoService 是网关可达形式（rpc.Args / rpc.Reply 参数）的回显服务。
type spEchoService struct {
	Module
	calls int
}

func newSPEchoService() *spEchoService {
	return &spEchoService{Module: Module{Name: "echo", Version: "1.0"}}
}

func (s *spEchoService) Startup() (bool, error) { return true, nil }

// Echo 把请求的 WSMessage 原样回写，Code 固定 7。
func (s *spEchoService) Echo(ctx context.Context, args *Args[*WSMessage], reply *Reply[*WSMessage]) error {
	s.calls++
	msg := args.GetData()
	if err := reply.SetData(msg); err != nil {
		return err
	}
	reply.SetCode(7)
	return nil
}

// Boom 是 panic 路径。
func (s *spEchoService) Boom(ctx context.Context, args *Args[*WSMessage], reply *Reply[*WSMessage]) error {
	panic("boom")
}

// withLocalEchoDispatch 注册 echo 服务到本地 dispatcher 并开启单进程直通，返回恢复函数。
func withLocalEchoDispatch(t *testing.T, whitelist ...string) (*spEchoService, func()) {
	t.Helper()
	ResetLocalDispatcherForTest()
	svc := newSPEchoService()
	localDispatcher.Register(svc.GetName(), svc)
	localDispatchEnabled.Store(true)
	setLocalGateWhiteList(whitelist)
	return svc, func() {
		ResetLocalDispatcherForTest()
		resetLocalGateWhiteListForTest()
	}
}

// newEchoFrameData 构造 echo.Echo 的请求负载（WSMessage 的 pb 字节）。
func newEchoFrameData(t *testing.T, payload []byte) []byte {
	t.Helper()
	data, err := proto.Marshal(&WSMessage{Module: "m", ServiceName: "s", Data: payload, ReqId: 42})
	if err != nil {
		t.Fatalf("marshal WSMessage: %v", err)
	}
	return data
}

// ---- sendMessage 本地直通分支 ----

// TestSendMessage_LocalDispatch_Whitelist 验证单进程直通分支的登录态/白名单语义
// 与分布式路径一致：未登录 + 非白名单 → 拒绝；白名单 → 放行；已登录 → 放行。
func TestSendMessage_LocalDispatch_Whitelist(t *testing.T) {
	svc, cleanup := withLocalEchoDispatch(t /* 无白名单 */)
	defer cleanup()

	args := &Args[*WSMessage]{ByteData: newEchoFrameData(t, []byte("hi"))}
	reply := &Reply[*WSMessage]{}

	// 未登录 + 非白名单 → 拒绝
	ct := newBareConnectData()
	if err := sendMessage(ct, "echo", "Echo", args, reply); err == nil ||
		!strings.Contains(err.Error(), "用户未登录") {
		t.Fatalf("err = %v, want 用户未登录", err)
	}
	if svc.calls != 0 {
		t.Fatalf("service should not be reached, calls=%d", svc.calls)
	}

	// 加入白名单 → 放行
	setLocalGateWhiteList([]string{"echo.Echo"})
	if err := sendMessage(ct, "echo", "Echo", args, reply); err != nil {
		t.Fatalf("whitelisted err = %v", err)
	}
	if svc.calls != 1 {
		t.Fatalf("service calls = %d, want 1", svc.calls)
	}

	// 已登录（无白名单）→ 放行
	setLocalGateWhiteList(nil)
	ct2 := newBareConnectData()
	ct2.userId = "u1"
	if err := sendMessage(ct2, "echo", "Echo", args, reply); err != nil {
		t.Fatalf("logged-in err = %v", err)
	}
	if svc.calls != 2 {
		t.Fatalf("service calls = %d, want 2", svc.calls)
	}
}

// TestSendMessage_SingleProcess_ModuleMissingNoPanic 验证 P0-2 的"明确报错"半边：
// 单进程模式 + 目标模块未注册 + discovery/rpcClient 均不可用时，sendMessage 返回
// 明确错误而非 panic（原实现：getRPCClient → 对 nil discovery 调方法 → panic）。
func TestSendMessage_SingleProcess_ModuleMissingNoPanic(t *testing.T) {
	_, cleanup := withLocalEchoDispatch(t)
	defer cleanup()

	// 保存并清空全局 rpcClient，模拟单进程模式下 client 从未启动
	origClient := rpcClient
	rpcClient = nil
	defer func() { rpcClient = origClient }()

	ct := newBareConnectData()
	ct.userId = "u1"
	err := sendMessage(ct, "not-registered", "Foo",
		&Args[*WSMessage]{}, &Reply[*WSMessage]{})
	if err == nil {
		t.Fatalf("expected explicit error for missing module, got nil")
	}
	t.Logf("got expected error: %v", err)
}

// ---- localGateDispatch 桥接 ----

// TestLocalGateDispatch_TypeBridge 验证网关泛型（Args[protoreflect.ProtoMessage]）
// 与业务泛型（Args[*WSMessage]）之间按 ByteData/Code 字段桥接，等价于 rpcx 序列化。
func TestLocalGateDispatch_TypeBridge(t *testing.T) {
	svc, cleanup := withLocalEchoDispatch(t)
	defer cleanup()

	raw := newEchoFrameData(t, []byte("bridge-payload"))
	// 模拟 doLogic 的真实形参类型
	gateArgs := newGateArgsForTest(raw)
	gateReply := newGateReplyForTest()

	if err := localGateDispatch(context.Background(), "echo", "Echo", gateArgs, gateReply); err != nil {
		t.Fatalf("localGateDispatch err = %v", err)
	}
	if svc.calls != 1 {
		t.Fatalf("service calls = %d, want 1", svc.calls)
	}
	// handler 写入的 ByteData / Code 必须回拷到网关 reply
	if getReplyCodeForTest(gateReply) != 7 {
		t.Errorf("reply.Code = %d, want 7", getReplyCodeForTest(gateReply))
	}
	echoed := &WSMessage{}
	if err := proto.Unmarshal(getReplyBytesForTest(gateReply), echoed); err != nil {
		t.Fatalf("unmarshal echoed reply: %v", err)
	}
	if !bytes.Equal(echoed.Data, []byte("bridge-payload")) || echoed.ReqId != 42 {
		t.Errorf("echoed payload mismatch: %+v", echoed)
	}
}

// TestLocalGateDispatch_PanicRecovered 验证 handler panic 被转为 error
// （分布式路径 rpcx 有 recover，本地路径必须等价，否则 panic 杀死 logic goroutine）。
func TestLocalGateDispatch_PanicRecovered(t *testing.T) {
	_, cleanup := withLocalEchoDispatch(t)
	defer cleanup()

	err := localGateDispatch(context.Background(), "echo", "Boom",
		newGateArgsForTest(newEchoFrameData(t, nil)), newGateReplyForTest())
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("err = %v, want panic-wrapped error", err)
	}
}

// TestLocalGateDispatch_Errors 表驱动覆盖错误分支。
func TestLocalGateDispatch_Errors(t *testing.T) {
	_, cleanup := withLocalEchoDispatch(t)
	defer cleanup()

	cases := []struct {
		name    string
		module  string
		method  string
		wantErr error
	}{
		{"模块未注册", "ghost", "Echo", ErrLocalServiceNotRegistered},
		{"方法不存在", "echo", "Ghost", ErrLocalMethodNotFound},
		{"签名不符", "echo", "Startup", ErrLocalMethodBadSignature},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := localGateDispatch(context.Background(), c.module, c.method,
				newGateArgsForTest(nil), newGateReplyForTest())
			if !errors.Is(err, c.wantErr) {
				t.Errorf("err = %v, want %v", err, c.wantErr)
			}
		})
	}
}

// TestLocalGateDispatch_IncompatibleParam 验证目标方法参数不是 Args/Reply 形式时
// 返回明确错误（而不是 reflect.Call panic）。
func TestLocalGateDispatch_IncompatibleParam(t *testing.T) {
	ResetLocalDispatcherForTest()
	defer ResetLocalDispatcherForTest()
	// GateService.Login 的参数是 *LoginReq（无 ByteData 字段）
	localDispatcher.Register("gate", &GateService{})
	localDispatchEnabled.Store(true)

	err := localGateDispatch(context.Background(), "gate", "Login",
		newGateArgsForTest([]byte("x")), newGateReplyForTest())
	if err == nil || !strings.Contains(err.Error(), "不兼容") {
		t.Fatalf("err = %v, want 参数类型不兼容", err)
	}
}

// ---- 端到端：真实 TCP 客户端消息 ----

// readTCPResponseFrame 从 conn 流式读出一个完整响应帧（带超时）。
// F5 后默认是 v2 帧：[251][2][compress][code:4][mtSize:2][dataSize:4][mt][data]。
func readTCPResponseFrame(t *testing.T, conn net.Conn) (messageType string, payload []byte) {
	t.Helper()
	mt, payload, _ := readTCPResponseFrameWithCode(t, conn)
	return mt, payload
}

// readTCPResponseFrameWithCode 同 readTCPResponseFrame 并返回 v2 帧的 code 字段。
func readTCPResponseFrameWithCode(t *testing.T, conn net.Conn) (messageType string, payload []byte, code int32) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	head := make([]byte, 13)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatalf("read response head: %v", err)
	}
	if head[0] != responseMagicNumber || head[1] != byte(Logic) {
		t.Fatalf("unexpected v2 frame leading bytes: %v", head[:2])
	}
	if head[2] == 1 {
		t.Fatalf("unexpected compressed frame in test (payload too small to compress)")
	}
	code = int32(binary.BigEndian.Uint32(head[3:7]))
	mtSize := int(binary.BigEndian.Uint16(head[7:9]))
	dataSize := int(binary.BigEndian.Uint32(head[9:13]))
	body := make([]byte, mtSize+dataSize)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body[:mtSize]), body[mtSize:], code
}

// startEphemeralGateway 在 127.0.0.1 的随机端口上启动一个真实 TCPServer，返回地址。
func startEphemeralGateway(t *testing.T) (*TCPServer, string) {
	t.Helper()
	builder := newTCPBuilder()
	builder.WithPort("0") // 内核分配随机端口
	srv := newDefaultTCPServer(builder)
	srv.Run()

	// 等 listener 真正起来（Run 内是同步 ListenTCP，这里立即可读）
	deadline := time.Now().Add(2 * time.Second)
	for {
		srv.listenerMu.Lock()
		l := srv.tcpListener
		srv.listenerMu.Unlock()
		if l != nil {
			return srv, l.Addr().String()
		}
		if time.Now().After(deadline) {
			t.Fatalf("tcp listener not ready within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSingleProcessGateway_EndToEnd 是 D4 / P0-2 的核心验收回归：
// 单进程模式（discovery=nil、rpcClient 不启动）下，真实 TCP 客户端发一条 Logic 帧，
// 网关 doLogic → sendMessage → 本地直通 → 响应帧回到客户端。
// v3 审计：该路径原先首条消息即 nil-discovery panic，网关 100% 不可用。
func TestSingleProcessGateway_EndToEnd(t *testing.T) {
	_, cleanup := withLocalEchoDispatch(t, "echo.Echo")
	defer cleanup()

	srv, addr := startEphemeralGateway(t)
	defer func() { _ = srv.CloseListeners() }()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()

	// 发送 Logic 帧：echo.Echo + WSMessage 负载
	frame := EncodeTgfBinaryFrame("echo", "Echo", newEchoFrameData(t, []byte("e2e-hello")))
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	mt, payload := readTCPResponseFrame(t, conn)
	if mt != "echo.Echo" {
		t.Errorf("response messageType = %q, want echo.Echo", mt)
	}
	echoed := &WSMessage{}
	if err := proto.Unmarshal(payload, echoed); err != nil {
		t.Fatalf("unmarshal response payload: %v", err)
	}
	if !bytes.Equal(echoed.Data, []byte("e2e-hello")) {
		t.Errorf("echoed data = %q, want e2e-hello", echoed.Data)
	}
}

// TestTCPServer_CloseListeners 验证停机钩子语义：
//   - CloseListeners 后新连接被拒绝；
//   - 已建立的连接不受影响（心跳仍能往返）；
//   - 幂等（重复调用返回 nil）。
func TestTCPServer_CloseListeners(t *testing.T) {
	_, cleanup := withLocalEchoDispatch(t, "echo.Echo")
	defer cleanup()

	srv, addr := startEphemeralGateway(t)

	// 先建立一条连接，并完成一次心跳往返——确保连接已被 accept 并交给 handleConn
	// （若仅 Dial 成功，连接可能还在内核 backlog 里，关 listener 会把它一并 abort）。
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write(EncodeTgfHeartbeatFrame()); err != nil {
		t.Fatalf("write first heartbeat: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	// F5: v2 心跳响应是 [responseMagicNumber, Heartbeat] 两字节
	one := make([]byte, len(heartbeatData))
	if _, err := io.ReadFull(conn, one); err != nil {
		t.Fatalf("first heartbeat round trip: %v", err)
	}

	if err := srv.CloseListeners(); err != nil {
		t.Fatalf("CloseListeners err = %v", err)
	}
	// 幂等
	if err := srv.CloseListeners(); err != nil {
		t.Errorf("second CloseListeners err = %v, want nil", err)
	}

	// 新连接必须失败（listener 已关）。Windows 上立即 connection refused。
	if c2, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		// 某些平台 backlog 里可能残留——发数据应当失败
		_ = c2.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		buf := make([]byte, 1)
		if _, rerr := c2.Read(buf); rerr == nil {
			t.Errorf("new connection should be rejected after CloseListeners")
		}
		_ = c2.Close()
	}

	// 已建立的连接不受影响：心跳往返（F5: v2 心跳响应两字节 [251,1]）
	if _, err := conn.Write(EncodeTgfHeartbeatFrame()); err != nil {
		t.Fatalf("write heartbeat on existing conn: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, len(heartbeatData))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("existing conn should survive CloseListeners, read err = %v", err)
	}
	if !bytes.Equal(buf, heartbeatData) {
		t.Errorf("heartbeat response = %v, want %v", buf, heartbeatData)
	}
}

// TestGateService_StopAccept 验证 GateService 层的停机钩子委托与 nil 安全。
func TestGateService_StopAccept(t *testing.T) {
	// Startup 之前调用：安全返回 nil
	g := &GateService{}
	if err := g.StopAccept(); err != nil {
		t.Errorf("StopAccept before Startup err = %v, want nil", err)
	}

	// 挂载 fakeTCPService 后委托给 CloseListeners
	g2 := newTestGate(&fakeTCPService{})
	if err := g2.StopAccept(); err != nil {
		t.Errorf("StopAccept err = %v, want nil", err)
	}
}
