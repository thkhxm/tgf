package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F4 · 过载保护测试
//
// 验收口径（路线图 F4）：
//   1. MaxConnections 真实生效——超限连接被拒（handleConn 级 + 真实 TCP 端到端）；
//   2. 背压显式响应——reqChan 满 / 连接 inactive 时客户端收到 CodeGateBusy 限流码
//      （而非静默丢弃）；
//   3. WS SetReadLimit + 未认证连接初始 read deadline（WS/KCP）；
//   4. LogicSync __hash 网关注入——DoLogin 后连接 meta 携带 __hash=userId，
//      并能透传到业务 handler（按用户串行的服务端语义见 rpcx/server/logicsync_test.go）。
//
//2026/6/10
//***************************************************

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/thkhxm/rpcx/v2/share"
	"github.com/thkhxm/tgf"
	"golang.org/x/net/context"
)

// ---- 1. MaxConnections ----

// TestF4_MaxConnections_HandleConnRejectsOverLimit 在 handleConn 级验证统一入口的
// 连接数限制：max=2 时第三条连接被立即关闭、不进入 users 表、计入拒绝指标。
// TCP/WS/KCP 三入口都汇聚到 handleConn，本测试即覆盖共享计数路径。
func TestF4_MaxConnections_HandleConnRejectsOverLimit(t *testing.T) {
	p := withMemoryMetrics(t)
	resetGateOverloadMetricsOnceForTest()
	t.Cleanup(resetGateOverloadMetricsOnceForTest)
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()

	srv := newTestServer()
	srv.config.(*ServerConfig).WithMaxConnections(2)

	// 前两条连接正常进入
	mock1, done1, _ := startF3TestConn(t, srv)
	mock2, done2, _ := startF3TestConn(t, srv)

	// 第三条：handleConn 入口即拒绝——立即关闭、users 表不新增
	usersBefore := srv.users.Len()
	mock3 := newMockConn(false)
	done3 := make(chan struct{})
	go func() {
		srv.handleConn(mock3)
		close(done3)
	}()
	select {
	case <-done3:
	case <-time.After(2 * time.Second):
		t.Fatal("超限连接的 handleConn 未立即返回")
	}
	if !mock3.closed.Load() {
		t.Error("超限连接应被立即关闭")
	}
	if got := srv.users.Len(); got != usersBefore {
		t.Errorf("超限连接不应进入 users 表: before=%d after=%d", usersBefore, got)
	}
	if got := p.CounterValue("tgf_gate_connections_rejected_total"); got != 1 {
		t.Errorf("tgf_gate_connections_rejected_total = %v, want 1", got)
	}

	// 释放一条后配额回收，新连接可进入
	_ = mock1.Close()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("mock1 handleConn 未退出")
	}
	waitUntil(t, 2*time.Second, func() bool { return srv.connCount.Load() == 1 })
	mock4, done4, _ := startF3TestConn(t, srv)

	_ = mock2.Close()
	_ = mock4.Close()
	<-done2
	<-done4
}

// startGatewayWithBuilder 在随机端口启动一个真实 TCPServer（与
// startEphemeralGateway 一致，但允许自定义 builder）。
func startGatewayWithBuilder(t *testing.T, builder ITCPBuilder) (*TCPServer, string) {
	t.Helper()
	srv := newDefaultTCPServer(builder)
	srv.Run()
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

// heartbeatRoundTrip 发一帧心跳并读回响应（v2 两字节），证明连接已被 handleConn 接管。
func heartbeatRoundTrip(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write(EncodeTgfHeartbeatFrame()); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, len(heartbeatData))
	if _, err := readFull(conn, buf); err != nil {
		t.Fatalf("heartbeat round trip: %v", err)
	}
}

// waitGateIdle 等待网关全部 handleConn goroutine 退出（connCount 归零）。
// 涉及登录的 E2E 测试必须在测试体返回前调用——handleConn 的 defer 清理
// （afterNaturalDisconnect → loginCoord.ClearGateOwner）是异步的，若测试 defer
// 先恢复 package 级 loginCoord，会与之构成 data race。
func waitGateIdle(t *testing.T, srv *TCPServer) {
	t.Helper()
	waitUntil(t, 3*time.Second, func() bool { return srv.connCount.Load() == 0 })
}

// readFull 是 io.ReadFull 的本地包装（避免在多个测试文件重复 import 块改动）。
func readFull(conn net.Conn, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := conn.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

// TestF4_MaxConnections_RealTCP_E2E 是 F4 验收主用例之一：真实 TCP listener +
// MaxConnections=1，第二条连接被拒（读到 EOF/RST）；第一条断开后配额回收，
// 新连接恢复可用。
func TestF4_MaxConnections_RealTCP_E2E(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	builder := newTCPBuilder()
	builder.WithPort("0")
	builder.WithMaxConnections(1)
	srv, addr := startGatewayWithBuilder(t, builder)
	defer func() { _ = srv.CloseListeners() }()

	conn1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial conn1: %v", err)
	}
	defer conn1.Close()
	heartbeatRoundTrip(t, conn1) // 确认 conn1 已被 handleConn 接管并占用配额

	// 第二条连接：accept 后 handleConn 入口即关闭——客户端读必然失败
	conn2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial conn2: %v", err)
	}
	defer conn2.Close()
	_, _ = conn2.Write(EncodeTgfHeartbeatFrame())
	_ = conn2.SetReadDeadline(time.Now().Add(3 * time.Second))
	one := make([]byte, 1)
	if _, rerr := conn2.Read(one); rerr == nil {
		t.Fatal("超限连接不应收到任何响应（应被服务端关闭）")
	}

	// conn1 断开 → 配额回收 → conn3 可用
	_ = conn1.Close()
	waitUntil(t, 3*time.Second, func() bool { return srv.connCount.Load() == 0 })
	conn3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial conn3: %v", err)
	}
	defer conn3.Close()
	heartbeatRoundTrip(t, conn3)
}

// ---- 2. 背压显式响应 ----

// slowBlockService 是会阻塞 logic goroutine 的本地服务，用于把 reqChan 灌满。
type slowBlockService struct {
	Module
	entered chan struct{}
	release chan struct{}
}

func newSlowBlockService() *slowBlockService {
	return &slowBlockService{
		Module:  Module{Name: "slow", Version: "1.0"},
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (s *slowBlockService) Startup() (bool, error) { return true, nil }

func (s *slowBlockService) Block(ctx context.Context, args *Args[*WSMessage], reply *Reply[*WSMessage]) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	reply.SetCode(1)
	return nil
}

// TestF4_Backpressure_BusyCode_ReqChanFull_E2E 是 F4 验收主用例之二（"满载时
// 客户端收到明确错误码"）：慢后端占住 logic goroutine、reqChan(cap 16) 灌满后，
// 溢出请求收到 CodeGateBusy 限流响应而非静默丢失。
func TestF4_Backpressure_BusyCode_ReqChanFull_E2E(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	svc := newSlowBlockService()
	ResetLocalDispatcherForTest()
	localDispatcher.Register(svc.GetName(), svc)
	localDispatchEnabled.Store(true)
	setLocalGateWhiteList([]string{"slow.Block"})
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(svc.release)
		}
	}
	t.Cleanup(func() {
		releaseOnce()
		ResetLocalDispatcherForTest()
		resetLocalGateWhiteListForTest()
	})

	srv, addr := startEphemeralGateway(t)
	defer func() { _ = srv.CloseListeners() }()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()

	frame := EncodeTgfBinaryFrame("slow", "Block", newEchoFrameData(t, []byte("x")))

	// 第 1 帧：被 logic goroutine 取走并阻塞在 handler 里
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write frame 1: %v", err)
	}
	select {
	case <-svc.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("slow handler 未被进入")
	}

	// 第 2..17 帧灌满 reqChan(cap 16)；第 18 帧触发 default 分支 → 限流响应
	for i := 0; i < 17; i++ {
		if _, err := conn.Write(frame); err != nil {
			t.Fatalf("write frame %d: %v", i+2, err)
		}
	}

	// 第一个到达客户端的响应必须是限流帧（其余响应仍被 handler 阻塞）
	mt, _, code := readTCPResponseFrameWithCode(t, conn)
	if mt != "slow.Block" {
		t.Errorf("限流响应 messageType = %q, want slow.Block", mt)
	}
	if code != CodeGateBusy {
		t.Errorf("限流响应 code = %d, want %d (CodeGateBusy)", code, CodeGateBusy)
	}

	// 释放 handler，确认积压的 17 条正常响应全部返回（背压解除后服务恢复）。
	// 必须读完——否则 logic goroutine 在测试结束后仍在消化队列，其
	// incGateDropped/incGateRequest 调用会与下一个测试的 metrics reset 竞争。
	releaseOnce()
	for i := 0; i < 17; i++ {
		mt, _, code = readTCPResponseFrameWithCode(t, conn)
		if mt != "slow.Block" || code != 1 {
			t.Fatalf("恢复后的第 %d 条正常响应 = (%q, %d), want (slow.Block, 1)", i+1, mt, code)
		}
	}
	_ = conn.Close()
	waitGateIdle(t, srv)
}

// TestF4_Backpressure_BusyCode_InactiveConn 验证 doLogic 对 inactive（LoggingIn）
// 连接的丢弃路径同样回写限流码（Offlining 终态不回写）。
func TestF4_Backpressure_BusyCode_InactiveConn(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	srv := newTestServer()
	mock, done, tpl := startF3TestConn(t, srv)
	defer func() {
		_ = mock.Close()
		<-done
	}()

	ucd, ok := srv.users.Get(tpl)
	if !ok {
		t.Fatal("users 表中找不到连接")
	}
	u := ucd.(*UserConnectData)
	if !u.tryBeginLogin() {
		t.Fatal("无法把连接置为 LoggingIn")
	}

	// LoggingIn 状态下注入业务帧 → doLogic 丢弃并回写 CodeGateBusy
	mock.frames <- &FrameIn{MessageType: Logic, Module: "echo", Method: "Echo", Data: []byte("x")}

	select {
	case w := <-mock.writes:
		// mockConn(isWS=false) 编码为 v2 二进制帧：[251][2][compress][code:4]...
		if len(w) < 7 || w[0] != responseMagicNumber || w[1] != byte(Logic) {
			t.Fatalf("非预期帧: %v", w)
		}
		code := int32(uint32(w[3])<<24 | uint32(w[4])<<16 | uint32(w[5])<<8 | uint32(w[6]))
		if code != CodeGateBusy {
			t.Errorf("inactive 丢弃路径 code = %d, want %d", code, CodeGateBusy)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未收到限流响应帧")
	}
}

// ---- 3. WS SetReadLimit + 初始 read deadline ----

// startF4WSServer 起一个 httptest WS 服务端，把 upgrade 后的 IConn 通过 ready 递出。
func startF4WSServer(t *testing.T, deadLine, writeTimeout time.Duration) (*httptest.Server, chan IConn) {
	t.Helper()
	ready := make(chan IConn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fc, err := wsFramedConnFromRequest(w, r, deadLine, writeTimeout)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		ready <- fc
	}))
	t.Cleanup(srv.Close)
	return srv, ready
}

func dialF4WSClient(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestF4_WS_ReadLimit_OversizeFrameRejected 验证 SetReadLimit 生效：超限消息使
// ReadFrame 报错（连接走清理），杜绝"未认证连接发送任意大消息 → 全量读入内存"
// 的远程 OOM 向量（审计 P1）。
func TestF4_WS_ReadLimit_OversizeFrameRejected(t *testing.T) {
	origLimit := wsReadLimit
	wsReadLimit = 128
	t.Cleanup(func() { wsReadLimit = origLimit })

	srv, ready := startF4WSServer(t, 10*time.Second, time.Second)
	client := dialF4WSClient(t, srv)

	var server IConn
	select {
	case server = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("upgrade 未完成")
	}

	// 客户端发一条 4KB（> 128B 限制）二进制消息
	big := make([]byte, 4096)
	if err := client.WriteMessage(websocket.BinaryMessage, big); err != nil {
		t.Fatalf("client write: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := server.ReadFrame()
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("超限消息应使 ReadFrame 返回错误")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ReadFrame 未在 3s 内返回（读限制未生效）")
	}
}

// TestF4_WS_InitialReadDeadline_SilentConnTimesOut 验证 upgrade 后立即生效的初始
// read deadline：一字节不发的未认证连接在 deadline 内被断开，不再永久挂起
// reader goroutine（慢速连接耗尽攻击，审计 P1）。
func TestF4_WS_InitialReadDeadline_SilentConnTimesOut(t *testing.T) {
	srv, ready := startF4WSServer(t, 300*time.Millisecond, time.Second)
	_ = dialF4WSClient(t, srv) // 连接后保持静默

	var server IConn
	select {
	case server = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("upgrade 未完成")
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := server.ReadFrame()
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("静默连接的 ReadFrame 应超时报错")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("初始 read deadline 未生效：ReadFrame 仍在阻塞")
	}
}

// TestF4_KCP_InitialReadDeadline_SilentConnTimesOut 验证 KCP 连接构造时立即设置
// 初始 read deadline（newKCPFramedConn 接受任意 net.Conn，这里用 net.Pipe 驱动）。
func TestF4_KCP_InitialReadDeadline_SilentConnTimesOut(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })

	builder := NewKCPBuilder("0").WithDeadLineTime(300 * time.Millisecond)
	fc, err := newKCPFramedConn(c2, builder)
	if err != nil {
		t.Fatalf("newKCPFramedConn: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := fc.ReadFrame()
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("静默连接的 ReadFrame 应超时报错")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("KCP 初始 read deadline 未生效：ReadFrame 仍在阻塞")
	}
}

// ---- 4. LogicSync __hash 网关注入 ----

// hashCaptureService 捕获到达 handler 的 share.Context 中的 __hash meta。
type hashCaptureService struct {
	Module
	captured atomic.Value // string
}

func newHashCaptureService() *hashCaptureService {
	return &hashCaptureService{Module: Module{Name: "hashcap", Version: "1.0"}}
}

func (s *hashCaptureService) Startup() (bool, error) { return true, nil }

func (s *hashCaptureService) Capture(ctx context.Context, args *Args[*WSMessage], reply *Reply[*WSMessage]) error {
	if sc, ok := ctx.(*share.Context); ok {
		s.captured.Store(sc.GetReqMetaDataByKey(share.ContextKeyHash))
	}
	reply.SetCode(0)
	return nil
}

// TestF4_DoLogin_InjectsLogicSyncHash 验证（接线证明）：
//  1. DoLogin 在连接 meta 注入 __hash=userId（tgf.ContextKeyHash == share.ContextKeyHash）；
//  2. 该 meta 随网关主链路（doLogic→sendMessage→dispatch）原样到达业务 handler——
//     分布式模式下同一 map 经 rpcx ReqMetaData 透传，rpcx 服务端 LogicSync 按它
//     选串行锁槽（"同一用户串行、不同用户并行"语义见 rpcx/server/logicsync_test.go
//     TestLogicSync_SameHashSerial_DiffHashParallel）。
func TestF4_DoLogin_InjectsLogicSyncHash(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	svc := newHashCaptureService()
	ResetLocalDispatcherForTest()
	localDispatcher.Register(svc.GetName(), svc)
	localDispatchEnabled.Store(true)
	t.Cleanup(func() {
		ResetLocalDispatcherForTest()
		resetLocalGateWhiteListForTest()
	})

	srv := newTestServer()
	mock, done, tpl := startF3TestConn(t, srv)
	defer func() {
		_ = mock.Close()
		<-done
	}()

	const uid = "f4-hash-user"
	if _, err := srv.DoLogin(uid, tpl, ""); err != nil {
		t.Fatalf("DoLogin err = %v", err)
	}

	// 1. 连接 meta 已注入 __hash=userId
	ucd, _ := srv.users.Get(uid)
	if ucd == nil {
		t.Fatal("登录后 users 表无该用户")
	}
	if got := ucd.GetContextData().GetReqMetaDataByKey(tgf.ContextKeyHash); got != uid {
		t.Fatalf("DoLogin 后 meta __hash = %q, want %q", got, uid)
	}

	// 2. 经网关主链路到达 handler 的 ctx 携带同一 __hash
	mock.frames <- &FrameIn{MessageType: Logic, Module: "hashcap", Method: "Capture",
		Data: newEchoFrameData(t, []byte("h"))}
	waitUntil(t, 3*time.Second, func() bool {
		v, _ := svc.captured.Load().(string)
		return v != ""
	})
	if got, _ := svc.captured.Load().(string); got != uid {
		t.Errorf("handler 收到的 __hash = %q, want %q", got, uid)
	}
}
