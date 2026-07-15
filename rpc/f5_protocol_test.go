package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F5 · 协议修缮测试
//
// 验收口径（路线图 F5）：
//   1. 心跳/压缩首字节歧义修复（v2 协议引导头）+ 新旧协议兼容开关；
//   2. KCP AEAD 非法密钥 fail-fast（不再静默降级明文）；
//   3. 帧级防伪（会话密钥 MAC，与 D7 鉴权联动，可开关，默认随鉴权开启）。
// robot 客户端回归见 robot/protocol_regression_test.go。
//
//2026/6/10
//***************************************************

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	util2 "github.com/thkhxm/rpcx/v2/util"
)

// ---- 1. 协议版本位：v2 消歧 + 兼容开关 ----

// TestF5_ResponseProtocolV2_Disambiguation 是审计 P1（心跳 0x01 vs compress=1
// 字节流歧义）的直接回归：v2 下两类帧首字节恒为 responseMagicNumber，第二字节
// 是 messageType——流式解析无歧义；并演示 legacy v1 的歧义确实存在（修缮动机）。
func TestF5_ResponseProtocolV2_Disambiguation(t *testing.T) {
	big := bytes.Repeat([]byte("z"), compressMinSize) // 触发压缩 → compress=1

	// v2（默认）：心跳 [251,1]，压缩逻辑响应 [251,2,1,...]——首字节即可分流
	hb := heartbeatResponseFrame()
	logic := encodeBinaryResponseFrame("game.Big", 0, big)
	if !bytes.Equal(hb, []byte{responseMagicNumber, byte(Heartbeat)}) {
		t.Fatalf("v2 心跳帧 = %v, want [251 1]", hb)
	}
	if logic[0] != responseMagicNumber || logic[1] != byte(Logic) {
		t.Fatalf("v2 逻辑响应引导头 = %v, want [251 2]", logic[:2])
	}
	if logic[2] != 1 {
		t.Fatalf("v2 压缩位 = %d, want 1", logic[2])
	}
	if hb[0] == logic[0] && hb[1] == logic[1] {
		t.Fatal("v2 心跳与逻辑响应不可同头")
	}

	// legacy v1：歧义存在——心跳 [1] 与压缩响应首字节 compress=1 无法区分
	UseLegacyResponseProtocol(true)
	defer UseLegacyResponseProtocol(false)
	hbV1 := heartbeatResponseFrame()
	logicV1 := encodeBinaryResponseFrame("game.Big", 0, big)
	if len(hbV1) != 1 || hbV1[0] != byte(Heartbeat) {
		t.Fatalf("v1 心跳帧 = %v, want [1]", hbV1)
	}
	if logicV1[0] != hbV1[0] {
		t.Fatalf("v1 歧义复现失败：压缩响应首字节 %d 应与心跳 %d 相同（这正是 v2 修缮的动机）",
			logicV1[0], hbV1[0])
	}
}

// TestF5_EncodeBinaryResponseFrame_V2Layout 逐字段校验 v2 帧布局与 code/压缩往返。
func TestF5_EncodeBinaryResponseFrame_V2Layout(t *testing.T) {
	payload := []byte("tiny-payload")
	frame := encodeBinaryResponseFrame("mod.Method", CodeGateBusy, payload)

	if frame[0] != responseMagicNumber || frame[1] != byte(Logic) || frame[2] != 0 {
		t.Fatalf("v2 头三字节 = %v, want [251 2 0]", frame[:3])
	}
	code := int32(binary.BigEndian.Uint32(frame[3:7]))
	if code != CodeGateBusy {
		t.Errorf("code = %d, want %d", code, CodeGateBusy)
	}
	mtSize := int(binary.BigEndian.Uint16(frame[7:9]))
	dataSize := int(binary.BigEndian.Uint32(frame[9:13]))
	if mtSize != len("mod.Method") || dataSize != len(payload) {
		t.Fatalf("sizes = (%d,%d), want (%d,%d)", mtSize, dataSize, len("mod.Method"), len(payload))
	}
	if string(frame[13:13+mtSize]) != "mod.Method" || !bytes.Equal(frame[13+mtSize:], payload) {
		t.Errorf("method/data 段不符")
	}

	// 压缩往返
	big := bytes.Repeat([]byte("abcd"), compressMinSize)
	zipped := encodeBinaryResponseFrame("mod.Big", 7, big)
	if zipped[2] != 1 {
		t.Fatalf("大负载应压缩")
	}
	zMtSize := int(binary.BigEndian.Uint16(zipped[7:9]))
	unzipped, err := util2.Unzip(zipped[13+zMtSize:])
	if err != nil {
		t.Fatalf("unzip: %v", err)
	}
	if !bytes.Equal(unzipped, big) {
		t.Errorf("压缩往返内容不一致")
	}
}

// TestF5_LegacyProtocol_E2E_HeartbeatCompat 验证兼容开关端到端语义：legacy 模式下
// 真实网关对心跳回写单字节 0x01（旧客户端可继续工作的兼容窗口）。
func TestF5_LegacyProtocol_E2E_HeartbeatCompat(t *testing.T) {
	UseLegacyResponseProtocol(true)
	defer UseLegacyResponseProtocol(false)
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()

	srv, addr := startEphemeralGateway(t)
	defer func() { _ = srv.CloseListeners() }()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer closeRPCResource(t, "websocket", conn)
	if _, err := conn.Write(EncodeTgfHeartbeatFrame()); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	one := make([]byte, 1)
	if _, err := readFull(conn, one); err != nil {
		t.Fatalf("read: %v", err)
	}
	if one[0] != byte(Heartbeat) {
		t.Errorf("legacy 心跳响应 = %v, want [1]", one)
	}
}

// ---- 2. KCP AEAD fail-fast ----

// TestF5_WithAEADKey_InvalidLengthPanics 非法密钥长度必须在配置期 panic
// （原实现静默忽略 → 明文裸奔，审计 P2）。
func TestF5_WithAEADKey_InvalidLengthPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("WithAEADKey(16字节) 应 panic")
		} else if !strings.Contains(r.(string), "AEAD key") {
			t.Fatalf("panic 信息不符: %v", r)
		}
	}()
	NewKCPBuilder("0").WithAEADKey(make([]byte, 16))
}

// TestF5_WithAEADKey_NilIsExplicitPlaintext nil/空密钥仍是合法的显式明文模式。
func TestF5_WithAEADKey_NilIsExplicitPlaintext(t *testing.T) {
	b := NewKCPBuilder("0").WithAEADKey(nil)
	if b.AEADKey() != nil {
		t.Fatal("nil 密钥应保持明文模式")
	}
}

// TestF5_NewKCPFramedConn_InvalidKeyFailClosed 绕过 builder 校验（直接构造带非法
// 密钥的 config，模拟自定义 IKCPBuilder 实现）时，连接构造必须返回错误而不是
// 降级明文（fail-closed）。
func TestF5_NewKCPFramedConn_InvalidKeyFailClosed(t *testing.T) {
	bad := &KCPServerConfig{aeadKey: make([]byte, 16)}
	c1, c2 := net.Pipe()
	defer closeRPCResource(t, "first websocket", c1)
	defer closeRPCResource(t, "second websocket", c2)
	fc, err := newKCPFramedConn(c2, bad)
	if err == nil {
		t.Fatalf("非法密钥应返回错误, got conn=%v", fc)
	}
	if !strings.Contains(err.Error(), "fail-closed") {
		t.Errorf("错误信息应声明 fail-closed: %v", err)
	}
}

// TestF5_StartKCPListener_InvalidKeyRefusesStart listener 启动前的兜底校验：
// 自定义 builder 携带非法密钥时拒绝启动 KCP listener。
func TestF5_StartKCPListener_InvalidKeyRefusesStart(t *testing.T) {
	srv := newTestServer()
	bad := &KCPServerConfig{address: "127.0.0.1", port: "0", aeadKey: make([]byte, 16)}
	if err := srv.startKCPListener(bad); err == nil {
		t.Fatal("非法密钥应拒绝启动 KCP listener")
	}
}

// ---- 3. 帧级防伪（会话密钥 MAC） ----

// TestF5_FrameMAC_ModeSwitch 开关语义：默认 Auto 随登录鉴权（D7 默认开启），
// 鉴权显式关闭则 MAC 一并关闭；On/Off 强制覆盖。
func TestF5_FrameMAC_ModeSwitch(t *testing.T) {
	t.Cleanup(func() {
		SetFrameMACMode(FrameMACAuto)
		loginCheckDisabled.Store(false)
	})

	SetFrameMACMode(FrameMACAuto)
	loginCheckDisabled.Store(false)
	if !frameMACActive() {
		t.Error("Auto + 鉴权开启 → MAC 应开启（默认随鉴权开启）")
	}
	loginCheckDisabled.Store(true)
	if frameMACActive() {
		t.Error("Auto + 鉴权关闭 → MAC 应关闭")
	}
	SetFrameMACMode(FrameMACOn)
	if !frameMACActive() {
		t.Error("强制 On 应覆盖鉴权关闭")
	}
	SetFrameMACMode(FrameMACOff)
	loginCheckDisabled.Store(false)
	if frameMACActive() {
		t.Error("强制 Off 应覆盖鉴权开启")
	}
}

// TestF5_FrameMAC_EncodeDecodeVerify MAC 帧编码→（TCP/KCP 两套解码）→校验往返，
// 以及篡改/重放/缺失的拒绝路径。
func TestF5_FrameMAC_EncodeDecodeVerify(t *testing.T) {
	key := DeriveFrameMACKey("resume-token-1")
	if len(key) != 32 {
		t.Fatalf("派生密钥长度 = %d, want 32", len(key))
	}
	if !bytes.Equal(key, DeriveFrameMACKey("resume-token-1")) {
		t.Fatal("同一 token 派生应确定")
	}
	if bytes.Equal(key, DeriveFrameMACKey("resume-token-2")) {
		t.Fatal("不同 token 派生应不同")
	}

	payload := []byte("mac-payload")
	frame := EncodeTgfBinaryMACFrame("m", "Act", payload, 7, key)

	// KCP 整帧解码路径
	fi, err := decodeTgfBinaryFrame(frame)
	if err != nil {
		t.Fatalf("decodeTgfBinaryFrame: %v", err)
	}
	if fi.MessageType != Logic || fi.Module != "m" || fi.Method != "Act" ||
		!bytes.Equal(fi.Data, payload) || fi.Seq != 7 || len(fi.MAC) != FrameMACSize {
		t.Fatalf("MAC 帧解码字段不符: %+v", fi)
	}

	// TCP 流式解码路径产出一致
	c1, c2 := net.Pipe()
	defer closeRPCResource(t, "first websocket", c1)
	defer closeRPCResource(t, "second websocket", c2)
	tc := newTCPFramedConn(c2, 0, 0)
	go func() { _, _ = c1.Write(frame) }()
	fi2, err := tc.ReadFrame()
	if err != nil {
		t.Fatalf("tcp ReadFrame: %v", err)
	}
	if fi2.Seq != fi.Seq || !bytes.Equal(fi2.MAC, fi.MAC) || !bytes.Equal(fi2.Data, fi.Data) {
		t.Fatalf("流式与整帧解码不一致: %+v vs %+v", fi2, fi)
	}

	// 校验语义（checkInboundFrameAuth 是 handleConn 的真实接线点）
	u := newBareConnectData()
	u.state.Store(int32(sessionOnline))
	u.macKey = key
	if !checkInboundFrameAuth(u, fi) {
		t.Fatal("合法 MAC 帧应通过校验")
	}
	if checkInboundFrameAuth(u, fi) {
		t.Fatal("重放同一 seq 应被拒绝")
	}

	// 篡改 data → 拒绝
	tampered := EncodeTgfBinaryMACFrame("m", "Act", payload, 8, key)
	tfi, _ := decodeTgfBinaryFrame(tampered)
	tfi.Data = []byte("evil-payload")
	if checkInboundFrameAuth(u, tfi) {
		t.Fatal("篡改后的帧应被拒绝")
	}

	// 错误密钥 → 拒绝
	wrongKey := EncodeTgfBinaryMACFrame("m", "Act", payload, 9, DeriveFrameMACKey("other"))
	wfi, _ := decodeTgfBinaryFrame(wrongKey)
	if checkInboundFrameAuth(u, wfi) {
		t.Fatal("错误密钥的帧应被拒绝")
	}

	// 已启 MAC 的会话收到无 MAC 帧 → 拒绝
	plain, _ := decodeTgfBinaryFrame(EncodeTgfBinaryFrame("m", "Act", payload))
	if checkInboundFrameAuth(u, plain) {
		t.Fatal("启用 MAC 后无 MAC 帧应被拒绝")
	}

	// 未启 MAC 的会话：无 MAC 帧放行；带 MAC 帧（无键可验）拒绝
	u2 := newBareConnectData()
	if !checkInboundFrameAuth(u2, plain) {
		t.Fatal("未启用 MAC 时无 MAC 帧应放行")
	}
	if checkInboundFrameAuth(u2, fi) {
		t.Fatal("未启用 MAC 时收到 MAC 帧（无键可验）应拒绝")
	}
}

// TestF5_FrameMAC_E2E_TCPGateway 帧级防伪端到端（接线证明）：真实 TCP 网关 +
// 本地 echo 服务，MAC 强制开启：
//  1. DoLogin 派生会话密钥（与 D7/F3 登录链路联动）；
//  2. 合法 MAC 帧正常到达业务并返回响应；
//  3. 重放帧导致连接被断开 + tgf_gate_mac_reject_total 计数；
//  4. 登录后改发无 MAC 帧同样被断开。
func TestF5_FrameMAC_E2E_TCPGateway(t *testing.T) {
	p := withMemoryMetrics(t)
	resetGateOverloadMetricsOnceForTest()
	t.Cleanup(resetGateOverloadMetricsOnceForTest)
	SetFrameMACMode(FrameMACOn)
	t.Cleanup(func() { SetFrameMACMode(FrameMACAuto) })
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	_, cleanup := withLocalEchoDispatch(t, "echo.Echo")
	defer cleanup()

	srv, addr := startEphemeralGateway(t)
	defer func() { _ = srv.CloseListeners() }()

	// 连接 + 等待 handleConn 登记，然后直接驱动 DoLogin（gate.Login 的会话绑定段）
	dialAndLogin := func(uid string) (net.Conn, []byte) {
		t.Helper()
		before := map[string]bool{}
		srv.users.Range(func(k string, _ IUserConnectData) bool { before[k] = true; return true })
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		heartbeatRoundTrip(t, conn) // 确认连接已被 handleConn 接管
		var tpl string
		waitUntil(t, 3*time.Second, func() bool {
			srv.users.Range(func(k string, _ IUserConnectData) bool {
				if !before[k] {
					tpl = k
					return false
				}
				return true
			})
			return tpl != ""
		})
		token, err := srv.DoLogin(uid, tpl, "")
		if err != nil {
			t.Fatalf("DoLogin: %v", err)
		}
		return conn, DeriveFrameMACKey(token)
	}

	// 1+2. 合法 MAC 帧 → 业务响应正常返回
	conn1, key1 := dialAndLogin("f5-mac-u1")
	macFrame := EncodeTgfBinaryMACFrame("echo", "Echo", newEchoFrameData(t, []byte("mac-ok")), 1, key1)
	if _, err := conn1.Write(macFrame); err != nil {
		t.Fatalf("write mac frame: %v", err)
	}
	mt, _, code := readTCPResponseFrameWithCode(t, conn1)
	if mt != "echo.Echo" || code != 7 {
		t.Fatalf("MAC 帧业务响应 = (%q,%d), want (echo.Echo,7)", mt, code)
	}

	// 3. 重放（seq=1 再次发送）→ 连接被断开 + 指标计数
	if _, err := conn1.Write(macFrame); err != nil {
		t.Fatalf("write replay frame: %v", err)
	}
	_ = conn1.SetReadDeadline(time.Now().Add(3 * time.Second))
	one := make([]byte, 1)
	if _, rerr := conn1.Read(one); rerr == nil {
		t.Fatal("重放帧后连接应被服务端断开")
	}
	waitUntil(t, 2*time.Second, func() bool {
		return p.CounterValue("tgf_gate_mac_reject_total") >= 1
	})

	// 4. 第二条连接登录后改发无 MAC 的 Logic 帧 → 同样被断开
	conn2, _ := dialAndLogin("f5-mac-u2")
	plain := EncodeTgfBinaryFrame("echo", "Echo", newEchoFrameData(t, []byte("no-mac")))
	if _, err := conn2.Write(plain); err != nil {
		t.Fatalf("write plain frame: %v", err)
	}
	_ = conn2.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, rerr := conn2.Read(one); rerr == nil {
		t.Fatal("登录后无 MAC 帧应导致连接被断开")
	}
	if got := p.CounterValue("tgf_gate_mac_reject_total"); got < 2 {
		t.Errorf("tgf_gate_mac_reject_total = %v, want >= 2", got)
	}

	// 两条连接的 handleConn defer（含 loginCoord.ClearGateOwner）必须在测试 defer
	// 恢复 loginCoord 之前完成，否则 -race 报包级变量读写竞争。
	waitGateIdle(t, srv)
}

// TestF5_FrameMAC_OffMode_PlainFramesAccepted MAC 关闭（兼容窗口）时登录后的
// 无 MAC 帧照常工作——开关的另一半语义。
func TestF5_FrameMAC_OffMode_PlainFramesAccepted(t *testing.T) {
	SetFrameMACMode(FrameMACOff)
	t.Cleanup(func() { SetFrameMACMode(FrameMACAuto) })
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	_, cleanup := withLocalEchoDispatch(t, "echo.Echo")
	defer cleanup()

	srv, addr := startEphemeralGateway(t)
	defer func() { _ = srv.CloseListeners() }()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer closeRPCResource(t, "websocket", conn)
	heartbeatRoundTrip(t, conn)

	var tpl string
	waitUntil(t, 3*time.Second, func() bool {
		srv.users.Range(func(k string, _ IUserConnectData) bool { tpl = k; return false })
		return tpl != ""
	})
	if _, err := srv.DoLogin("f5-nomac-u", tpl, ""); err != nil {
		t.Fatalf("DoLogin: %v", err)
	}

	plain := EncodeTgfBinaryFrame("echo", "Echo", newEchoFrameData(t, []byte("plain-ok")))
	if _, err := conn.Write(plain); err != nil {
		t.Fatalf("write: %v", err)
	}
	mt, _, code := readTCPResponseFrameWithCode(t, conn)
	if mt != "echo.Echo" || code != 7 {
		t.Errorf("MAC 关闭时无 MAC 帧响应 = (%q,%d), want (echo.Echo,7)", mt, code)
	}

	// 已登录连接的 handleConn defer 会读 loginCoord——必须在测试 defer 恢复它
	// 之前完成（见 waitGateIdle 注释）。
	_ = conn.Close()
	waitGateIdle(t, srv)
}
