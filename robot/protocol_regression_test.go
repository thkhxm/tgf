package robot

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F5 · robot 客户端协议回归（v2/v1 下行帧解码 + 真实网关往返）
//
// 验收口径（路线图 F5）："robot 客户端回归 + 新旧协议兼容开关测试"：
//   - DecodeServerFrame / ReadServerFrame 对 v2（默认）与 legacy v1 帧的
//     字节级 golden 解码；
//   - 真实 TCP / KCP 网关心跳往返——心跳 0x01 与 compress=1 的字节流歧义
//     在 v2 协议下消除（robot 流式解码无需依赖"长度==1"的脆弱判断）；
//   - 帧级 MAC（LogicMAC）客户端编码布局。
//
//2026/6/10
//***************************************************

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	util2 "github.com/thkhxm/rpcx/v2/util"
	"github.com/thkhxm/tgf/v2/rpc"
)

// ---- golden 编码辅助：按 v2/v1 协议规格手工构帧（独立于服务端实现的字节级约定） ----

func buildV2LogicFrame(t *testing.T, messageType string, code int32, data []byte, compress bool) []byte {
	t.Helper()
	payload := data
	var cb byte
	if compress {
		zipped, err := util2.Zip(data)
		if err != nil {
			t.Fatalf("zip: %v", err)
		}
		payload = zipped
		cb = 1
	}
	frame := []byte{responseMagic, msgLogic, cb}
	var code4 [4]byte
	binary.BigEndian.PutUint32(code4[:], uint32(code))
	frame = append(frame, code4[:]...)
	var mt2 [2]byte
	binary.BigEndian.PutUint16(mt2[:], uint16(len(messageType)))
	frame = append(frame, mt2[:]...)
	var ds4 [4]byte
	binary.BigEndian.PutUint32(ds4[:], uint32(len(payload)))
	frame = append(frame, ds4[:]...)
	frame = append(frame, messageType...)
	frame = append(frame, payload...)
	return frame
}

func buildV1LogicFrame(t *testing.T, messageType string, data []byte) []byte {
	t.Helper()
	frame := []byte{0}
	var mt2 [2]byte
	binary.BigEndian.PutUint16(mt2[:], uint16(len(messageType)))
	frame = append(frame, mt2[:]...)
	var ds4 [4]byte
	binary.BigEndian.PutUint32(ds4[:], uint32(len(data)))
	frame = append(frame, ds4[:]...)
	frame = append(frame, messageType...)
	frame = append(frame, data...)
	return frame
}

// ---- 整帧解码（KCP 路径用） ----

func TestRobot_DecodeServerFrame_V2(t *testing.T) {
	// 心跳
	sf, err := DecodeServerFrame([]byte{responseMagic, msgHeartbeat})
	if err != nil || !sf.IsHeartbeat {
		t.Fatalf("v2 心跳解码 = (%+v, %v)", sf, err)
	}
	// 替换登录通知
	sf, err = DecodeServerFrame([]byte{responseMagic, msgReplace})
	if err != nil || !sf.IsReplaceKick {
		t.Fatalf("v2 替换登录解码 = (%+v, %v)", sf, err)
	}
	// 逻辑响应（带限流码）
	sf, err = DecodeServerFrame(buildV2LogicFrame(t, "mod.M", -429, []byte("busy"), false))
	if err != nil {
		t.Fatalf("v2 逻辑响应解码: %v", err)
	}
	if sf.MessageType != "mod.M" || sf.Code != -429 || !bytes.Equal(sf.Data, []byte("busy")) {
		t.Errorf("v2 逻辑响应字段不符: %+v", sf)
	}
	// 压缩逻辑响应——首字节是 251 而非 compress 位，与心跳无歧义（修缮核心）
	big := bytes.Repeat([]byte("p"), 4096)
	frame := buildV2LogicFrame(t, "mod.Big", 0, big, true)
	if frame[0] != responseMagic {
		t.Fatalf("v2 压缩帧首字节 = %d, want %d", frame[0], responseMagic)
	}
	sf, err = DecodeServerFrame(frame)
	if err != nil {
		t.Fatalf("v2 压缩解码: %v", err)
	}
	if !bytes.Equal(sf.Data, big) {
		t.Errorf("v2 压缩往返不一致")
	}
}

func TestRobot_DecodeServerFrame_LegacyV1(t *testing.T) {
	// v1 心跳：单字节 0x01
	sf, err := DecodeServerFrame([]byte{0x01})
	if err != nil || !sf.IsHeartbeat {
		t.Fatalf("v1 心跳解码 = (%+v, %v)", sf, err)
	}
	// v1 替换登录：单字节 0x03
	sf, err = DecodeServerFrame([]byte{0x03})
	if err != nil || !sf.IsReplaceKick {
		t.Fatalf("v1 替换登录解码 = (%+v, %v)", sf, err)
	}
	// v1 逻辑响应（compress=0）
	sf, err = DecodeServerFrame(buildV1LogicFrame(t, "mod.M", []byte("v1data")))
	if err != nil {
		t.Fatalf("v1 逻辑响应解码: %v", err)
	}
	if sf.MessageType != "mod.M" || sf.Code != 0 || !bytes.Equal(sf.Data, []byte("v1data")) {
		t.Errorf("v1 逻辑响应字段不符: %+v", sf)
	}
	// 请求格式心跳回显 [250,1]
	sf, err = DecodeServerFrame([]byte{requestMagic, 0x01})
	if err != nil || !sf.IsHeartbeat {
		t.Fatalf("请求回显心跳解码 = (%+v, %v)", sf, err)
	}
}

// ---- 流式解码（TCP 路径用） ----

func TestRobot_ReadServerFrame_StreamMixed(t *testing.T) {
	var stream bytes.Buffer
	stream.Write([]byte{responseMagic, msgHeartbeat})
	stream.Write(buildV2LogicFrame(t, "a.B", 7, []byte("one"), false))
	stream.Write([]byte{responseMagic, msgReplace})
	stream.Write(buildV2LogicFrame(t, "c.D", 0, bytes.Repeat([]byte("q"), 4096), true))

	r := bufio.NewReader(&stream)

	sf, err := ReadServerFrame(r)
	if err != nil || !sf.IsHeartbeat {
		t.Fatalf("帧1 应为心跳: (%+v, %v)", sf, err)
	}
	sf, err = ReadServerFrame(r)
	if err != nil || sf.MessageType != "a.B" || sf.Code != 7 || !bytes.Equal(sf.Data, []byte("one")) {
		t.Fatalf("帧2 解码不符: (%+v, %v)", sf, err)
	}
	sf, err = ReadServerFrame(r)
	if err != nil || !sf.IsReplaceKick {
		t.Fatalf("帧3 应为替换登录通知: (%+v, %v)", sf, err)
	}
	sf, err = ReadServerFrame(r)
	if err != nil || sf.MessageType != "c.D" || !bytes.Equal(sf.Data, bytes.Repeat([]byte("q"), 4096)) {
		t.Fatalf("帧4 压缩解码不符: (%+v, %v)", sf, err)
	}
}

func TestRobot_ReadServerFrame_StreamLegacyV1(t *testing.T) {
	var stream bytes.Buffer
	stream.WriteByte(0x01) // v1 心跳
	stream.Write(buildV1LogicFrame(t, "a.B", []byte("v1")))
	stream.WriteByte(0x03) // v1 替换登录

	r := bufio.NewReader(&stream)
	sf, err := ReadServerFrame(r)
	if err != nil || !sf.IsHeartbeat {
		t.Fatalf("v1 流式心跳: (%+v, %v)", sf, err)
	}
	sf, err = ReadServerFrame(r)
	if err != nil || sf.MessageType != "a.B" || !bytes.Equal(sf.Data, []byte("v1")) {
		t.Fatalf("v1 流式逻辑响应: (%+v, %v)", sf, err)
	}
	sf, err = ReadServerFrame(r)
	if err != nil || !sf.IsReplaceKick {
		t.Fatalf("v1 流式替换登录: (%+v, %v)", sf, err)
	}
}

// ---- 帧级 MAC 客户端编码布局 ----

func TestRobot_MACFrameLayout(t *testing.T) {
	key := rpc.DeriveFrameMACKey("robot-token")
	data := []byte("act")
	frame := rpc.EncodeTgfBinaryMACFrame("m", "Do", data, 3, key)

	if frame[0] != requestMagic || frame[1] != byte(rpc.LogicMAC) {
		t.Fatalf("MAC 帧引导字节 = %v, want [250 4]", frame[:2])
	}
	methodSize := int(binary.BigEndian.Uint16(frame[2:4]))
	dataSize := int(binary.BigEndian.Uint16(frame[4:6]))
	if methodSize != len("m.Do") || dataSize != len(data) {
		t.Fatalf("sizes = (%d,%d)", methodSize, dataSize)
	}
	seq := binary.BigEndian.Uint64(frame[6:14])
	if seq != 3 {
		t.Fatalf("seq = %d, want 3", seq)
	}
	want := 6 + 8 + methodSize + dataSize + rpc.FrameMACSize
	if len(frame) != want {
		t.Fatalf("帧总长 = %d, want %d", len(frame), want)
	}
}

// ---- 真实网关端到端回归 ----

// testTCPBuilder 是 robot 包侧的 rpc.ITCPBuilder 最小实现（rpc 包未导出默认
// builder 构造器，e2e 回归用它驱动 rpc.GatewayService）。
type testTCPBuilder struct {
	port    string
	maxConn int32
}

func (b *testTCPBuilder) WithPort(port string) rpc.ITCPBuilder   { b.port = port; return b }
func (b *testTCPBuilder) WithBuffer(r, w int) rpc.ITCPBuilder    { return b }
func (b *testTCPBuilder) WithWSPath(path string) rpc.ITCPBuilder { return b }
func (b *testTCPBuilder) Address() string                        { return "127.0.0.1" }
func (b *testTCPBuilder) Port() string                           { return b.port }
func (b *testTCPBuilder) WsPath() string                         { return "" }
func (b *testTCPBuilder) MaxConnections() int32                  { return b.maxConn }
func (b *testTCPBuilder) WithMaxConnections(max int32) rpc.ITCPBuilder {
	b.maxConn = max
	return b
}
func (b *testTCPBuilder) DeadLineTime() time.Duration { return 30 * time.Second }
func (b *testTCPBuilder) WriteTimeout() time.Duration { return 5 * time.Second }
func (b *testTCPBuilder) WithWriteTimeout(d time.Duration) rpc.ITCPBuilder {
	return b
}
func (b *testTCPBuilder) ReadBufferSize() int                      { return 1024 }
func (b *testTCPBuilder) WriteBufferSize() int                     { return 8 * 1024 }
func (b *testTCPBuilder) IsWebSocket() bool                        { return false }
func (b *testTCPBuilder) WithWss(cert, key string) rpc.ITCPBuilder { return b }
func (b *testTCPBuilder) IsWss() bool                              { return false }
func (b *testTCPBuilder) WssCertFile() string                      { return "" }
func (b *testTCPBuilder) WssKeyFile() string                       { return "" }

// allocFreePort 申请一个空闲 TCP 端口。
func allocFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("alloc port: %v", err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

// allocFreeUDPPort 申请一个空闲 UDP 端口（KCP 用）。
func allocFreeUDPPort(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("alloc udp port: %v", err)
	}
	defer conn.Close()
	_, port, _ := net.SplitHostPort(conn.LocalAddr().String())
	return port
}

// stopAccepter 投影 GateService 的停机钩子。
type stopAccepter interface{ StopAccept() error }

// TestRobot_TCP_HeartbeatRoundtrip_V2 真实 TCP 网关心跳往返：服务端 v2 响应帧
// [251,1] 被 robot 流式解码器正确识别为心跳——这正是审计 P1"心跳 0x01 与
// compress=1 歧义"在新协议下的消除证明（v1 时代 robot 只能靠脆弱的首字节猜测）。
func TestRobot_TCP_HeartbeatRoundtrip_V2(t *testing.T) {
	port := allocFreePort(t)
	gw := rpc.GatewayService(&testTCPBuilder{port: port})
	if _, err := gw.Startup(); err != nil {
		t.Fatalf("gateway startup: %v", err)
	}
	if sa, ok := gw.(stopAccepter); ok {
		t.Cleanup(func() { _ = sa.StopAccept() })
	}

	// 等 listener 就绪
	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
		if err == nil {
			conn = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway 未就绪: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer conn.Close()

	if _, err := conn.Write(rpc.EncodeTgfHeartbeatFrame()); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	sf, err := ReadServerFrame(bufio.NewReader(conn))
	if err != nil {
		t.Fatalf("ReadServerFrame: %v", err)
	}
	if !sf.IsHeartbeat {
		t.Errorf("应解码为心跳帧: %+v", sf)
	}
}

// TestRobot_KCP_HeartbeatRoundtrip 真实 KCP 网关心跳往返：覆盖 KCPClient 的
// ReadServerFrame（ReadRawFrame + DecodeServerFrame）修复——原实现用请求格式
// 解码服务端响应，格式不匹配帧即被消费丢弃（v3 审计）。
func TestRobot_KCP_HeartbeatRoundtrip(t *testing.T) {
	tcpPort := allocFreePort(t)
	kcpPort := allocFreeUDPPort(t)
	gw := rpc.GatewayServiceWithKCP(&testTCPBuilder{port: tcpPort}, rpc.NewKCPBuilder(kcpPort))
	if _, err := gw.Startup(); err != nil {
		t.Fatalf("gateway startup: %v", err)
	}
	if sa, ok := gw.(stopAccepter); ok {
		t.Cleanup(func() { _ = sa.StopAccept() })
	}

	client, err := DialKCP(context.Background(), "127.0.0.1:"+kcpPort, nil)
	if err != nil {
		t.Fatalf("DialKCP: %v", err)
	}
	defer client.Close()

	if err := client.SendHeartbeat(); err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	sf, err := client.ReadServerFrame()
	if err != nil {
		t.Fatalf("ReadServerFrame: %v", err)
	}
	if !sf.IsHeartbeat {
		t.Errorf("应解码为心跳帧: %+v", sf)
	}
}

// TestRobot_FrameMACCapable_Implemented 编译期+运行期确认 tcp 与 kcp robot 都实现
// FrameMACCapable（帧级 MAC 升级入口）。
func TestRobot_FrameMACCapable_Implemented(t *testing.T) {
	var r IRobot = NewRobotTcp()
	if _, ok := r.(FrameMACCapable); !ok {
		t.Error("tcp robot 应实现 FrameMACCapable")
	}
	var k IRobot = NewRobotKCP(nil)
	if _, ok := k.(FrameMACCapable); !ok {
		t.Error("kcp robot 应实现 FrameMACCapable")
	}
}
