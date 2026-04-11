package rpc

// A8: KCP 网关支持。
//
// KCP 作为 IConn 的第三个实现，和 TCP/WS 共享 handleConn 的连接生命周期。
// 具体做法：GateService 在 Startup 阶段除了启动 TCP/WS listener，如果配了
// KCPBuilder 就额外起一个 KCP listener accept 循环；accept 到的 kcp.UDPSession
// 包装成 kcpFramedConn（IConn）后扔进 tcpService.handleConn，连接后续生命周期
// 完全复用 A2-phase2 的 reader/logic/writer 三 goroutine 模型。
//
// 帧格式：外层每帧 = [4B big-endian 长度][payload]。payload 在 AEAD 开启时是
// nonce+sealed，关闭时就是原始 tgf 二进制帧。详见 frame_crypto.go。
//
// unreliable 通道（不可靠快速通道）：A8 只做"接口和帧头位预留"——
//   - FrameIn 暂不加 Channel 字段（当前全部 reliable，不会歧义）
//   - 真正实现需要旁路 UDPConn + session id 分发，工作量大，放 C 档或业务需要时再补

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"github.com/xtaci/kcp-go"
)

// ---- Builder ----

// IKCPBuilder 是 KCP 网关的配置入口。与 ITCPBuilder 对等但参数不同。
type IKCPBuilder interface {
	Address() string
	Port() string
	// ReadBufferSize / WriteBufferSize 是 UDP 内核缓冲，与 KCP 自己的
	// send/recv window 不同——后者由 NoDelay/Interval/Resend/Nc 四元组决定。
	ReadBufferSize() int
	WriteBufferSize() int
	// DeadLineTime 是连接 idle read deadline，和 TCP 语义一致。
	DeadLineTime() time.Duration
	WriteTimeout() time.Duration
	// NoDelay / Interval / Resend / Nc 是 KCP 核心参数。
	//   (1, 10, 2, 1) 是 "fast mode" 低延迟配置
	//   (0, 40, 0, 0) 是 "normal mode" 默认配置
	NoDelay() int
	Interval() int
	Resend() int
	Nc() int
	// AEADKey 返回预共享 32 字节密钥。nil 时走明文模式。
	AEADKey() []byte
}

// KCPServerConfig 是 IKCPBuilder 的默认实现，builder pattern 方便链式配置。
type KCPServerConfig struct {
	address         string
	port            string
	readBufferSize  int
	writeBufferSize int
	deadLineTime    time.Duration
	writeTimeout    time.Duration
	noDelay         int
	interval        int
	resend          int
	nc              int
	aeadKey         []byte
}

func (k *KCPServerConfig) Address() string            { return k.address }
func (k *KCPServerConfig) Port() string               { return k.port }
func (k *KCPServerConfig) ReadBufferSize() int        { return k.readBufferSize }
func (k *KCPServerConfig) WriteBufferSize() int       { return k.writeBufferSize }
func (k *KCPServerConfig) DeadLineTime() time.Duration {
	if k.deadLineTime <= 0 {
		return defaultDeadLineTime
	}
	return k.deadLineTime
}
func (k *KCPServerConfig) WriteTimeout() time.Duration {
	if k.writeTimeout <= 0 {
		return defaultWriteDeadline
	}
	return k.writeTimeout
}
func (k *KCPServerConfig) NoDelay() int   { return k.noDelay }
func (k *KCPServerConfig) Interval() int  { return k.interval }
func (k *KCPServerConfig) Resend() int    { return k.resend }
func (k *KCPServerConfig) Nc() int        { return k.nc }
func (k *KCPServerConfig) AEADKey() []byte { return k.aeadKey }

// NewKCPBuilder 返回一个默认参数的 KCP builder。
// 默认 fast mode 参数 (NoDelay=1, Interval=10, Resend=2, Nc=1)，适合低延迟游戏场景。
// AEAD 默认关闭——生产必须通过 WithAEADKey 显式开启。
func NewKCPBuilder(port string) *KCPServerConfig {
	return &KCPServerConfig{
		address:         defaultIp,
		port:            port,
		readBufferSize:  4 * 1024 * 1024,
		writeBufferSize: 4 * 1024 * 1024,
		deadLineTime:    defaultDeadLineTime,
		writeTimeout:    defaultWriteDeadline,
		noDelay:         1,
		interval:        10,
		resend:          2,
		nc:              1,
	}
}

// WithAEADKey 设置预共享 32 字节密钥。传 nil 或长度不对会忽略。
// A8: 生产环境必须调用此方法开启加密；不调用则走明文（仅限开发/内网）。
func (k *KCPServerConfig) WithAEADKey(key []byte) *KCPServerConfig {
	if len(key) == aeadKeySize {
		k.aeadKey = key
	}
	return k
}

// WithKCPParams 覆盖 KCP 的 NoDelay/Interval/Resend/Nc 四元组。
func (k *KCPServerConfig) WithKCPParams(noDelay, interval, resend, nc int) *KCPServerConfig {
	k.noDelay = noDelay
	k.interval = interval
	k.resend = resend
	k.nc = nc
	return k
}

// WithDeadLineTime / WithWriteTimeout 与 ServerConfig 对齐
func (k *KCPServerConfig) WithDeadLineTime(d time.Duration) *KCPServerConfig {
	if d > 0 {
		k.deadLineTime = d
	}
	return k
}

func (k *KCPServerConfig) WithWriteTimeout(d time.Duration) *KCPServerConfig {
	if d > 0 {
		k.writeTimeout = d
	}
	return k
}

// ---- IConn 实现 ----

// kcpFramedConn 包装一个 KCP 会话（net.Conn 接口，底层是 *kcp.UDPSession）
// 为 IConn。写入/读取都走 frame_crypto.go 里的 4 字节长度前缀 + 可选 AEAD。
type kcpFramedConn struct {
	conn         net.Conn
	sealer       aeadSealer
	deadLineTime time.Duration
	writeTimeout time.Duration
	remote       string
	// writeMu 保护 writeKCPFrame 的写入——一个 KCP session 同时只能有一个
	// writer goroutine（handleConn 只起一个 writer）但 Offline 路径可能也写
	// close 帧，互斥一下保险。
	writeMu sync.Mutex
}

func newKCPFramedConn(conn net.Conn, builder IKCPBuilder) *kcpFramedConn {
	var sealer aeadSealer = plaintextSealer{}
	if key := builder.AEADKey(); len(key) == aeadKeySize {
		if cs, err := newChaChaSealer(key); err == nil {
			sealer = cs
		} else {
			log.WarnTag("init", "KCP AEAD 初始化失败,降级明文模式 err=%v", err)
		}
	}
	return &kcpFramedConn{
		conn:         conn,
		sealer:       sealer,
		deadLineTime: builder.DeadLineTime(),
		writeTimeout: builder.WriteTimeout(),
		remote:       conn.RemoteAddr().String(),
	}
}

// ReadFrame 读一帧：先读外层长度前缀 + AEAD payload，Open 后再按 tgf
// 二进制协议解码得到 FrameIn。
func (c *kcpFramedConn) ReadFrame() (*FrameIn, error) {
	plaintext, err := readKCPFrame(c.conn, c.sealer)
	if err != nil {
		return nil, err
	}
	// plaintext 是 tgf 二进制协议帧，用 tcpFramedConn 同一套解码逻辑
	return decodeTgfBinaryFrame(plaintext)
}

func (c *kcpFramedConn) WriteFrame(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	return writeKCPFrame(c.conn, c.sealer, data)
}

func (c *kcpFramedConn) Close() error                        { return c.conn.Close() }
func (c *kcpFramedConn) RemoteAddr() string                  { return c.remote }
func (c *kcpFramedConn) SetReadDeadline(t time.Time) error   { return c.conn.SetReadDeadline(t) }
func (c *kcpFramedConn) SetWriteDeadline(t time.Time) error  { return c.conn.SetWriteDeadline(t) }

// IsWebSocket 返回 false：KCP 走和 TCP 一样的二进制编码路径（getSendToClientData
// 的 TCP 分支），不是 WebSocket 的 WSMessage 协议。
func (c *kcpFramedConn) IsWebSocket() bool { return false }

// ---- 二进制帧解码（从 tcpFramedConn 抽出来供 kcpFramedConn 复用）----

// decodeTgfBinaryFrame 解析一个完整的 tgf 二进制帧的原始字节（不含外层长度前缀、
// 不含 AEAD 包装）。兼容 tcpFramedConn.ReadFrame 使用的协议：
//
//	[1 magic][1 msgType] { [2 methodSize][2 dataSize][n method][n data] }
//
// Heartbeat 帧只有前两字节；Logic 帧有完整头和负载。
func decodeTgfBinaryFrame(raw []byte) (*FrameIn, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("tgf/rpc: frame too short: %d bytes", len(raw))
	}
	if raw[0] != requestMagicNumber {
		return nil, fmt.Errorf("tgf/rpc: magic mismatch, got %v want %v", raw[0], requestMagicNumber)
	}
	msgType := raw[1]
	switch msgType {
	case byte(Heartbeat):
		return &FrameIn{MessageType: Heartbeat}, nil
	case byte(Logic):
		if len(raw) < int(requestHeadSize) {
			return nil, fmt.Errorf("tgf/rpc: logic frame header truncated")
		}
		// 复用 encoding/binary 解析头部（和 tcpFramedConn 完全一致）
		methodSize := uint16(raw[2])<<8 | uint16(raw[3])
		dataSize := uint16(raw[4])<<8 | uint16(raw[5])
		expected := int(requestHeadSize) + int(methodSize) + int(dataSize)
		if len(raw) < expected {
			return nil, fmt.Errorf("tgf/rpc: logic frame truncated: have %d want %d", len(raw), expected)
		}
		reqNameIndex := int(requestHeadSize) + int(methodSize)
		reqName := util.ConvertStringByByteSlice(raw[int(requestHeadSize):reqNameIndex])
		ix := strings.LastIndex(reqName, ".")
		if ix < 0 {
			return nil, fmt.Errorf("tgf/rpc: malformed method name %q", reqName)
		}
		return &FrameIn{
			MessageType: Logic,
			Module:      reqName[:ix],
			Method:      reqName[ix+1:],
			Data:        raw[reqNameIndex:expected],
		}, nil
	default:
		return nil, fmt.Errorf("tgf/rpc: unknown message type %d", msgType)
	}
}

// ---- Server 侧 accept loop ----

// startKCPListener 由 GateService.Startup 调用。如果 builder 为 nil 直接返回。
// 返回 error 表示 listener 创建失败；accept loop 内部错误只打日志不返回。
func (t *TCPServer) startKCPListener(builder IKCPBuilder) error {
	if builder == nil {
		return nil
	}
	// KCP 的 block 参数是 BlockCrypt，我们用自己的 AEAD 层，所以传 nil。
	// 两个 shards 参数是 FEC，A8 先不开（dataShards=0, parityShards=0）。
	addr := fmt.Sprintf("%s:%s", builder.Address(), builder.Port())
	listener, err := kcp.ListenWithOptions(addr, nil, 0, 0)
	if err != nil {
		return fmt.Errorf("tgf/rpc: kcp listen %s: %w", addr, err)
	}
	if builder.ReadBufferSize() > 0 {
		_ = listener.SetReadBuffer(builder.ReadBufferSize())
	}
	if builder.WriteBufferSize() > 0 {
		_ = listener.SetWriteBuffer(builder.WriteBufferSize())
	}
	log.InfoTag("init", "KCP网关启动成功 addr=%s aead=%v",
		addr, len(builder.AEADKey()) == aeadKeySize)

	util.Go(func() {
		for {
			session, acceptErr := listener.AcceptKCP()
			if acceptErr != nil {
				log.DebugTag("tcp", "KCP AcceptKCP error: %v", acceptErr)
				return
			}
			// 应用 KCP 参数（每 session 配置）
			session.SetNoDelay(builder.NoDelay(), builder.Interval(), builder.Resend(), builder.Nc())
			session.SetStreamMode(true) // 流式模式匹配我们的长度前缀帧协议
			session.SetACKNoDelay(true)
			if builder.ReadBufferSize() > 0 {
				_ = session.SetReadBuffer(builder.ReadBufferSize())
			}
			if builder.WriteBufferSize() > 0 {
				_ = session.SetWriteBuffer(builder.WriteBufferSize())
			}

			fc := newKCPFramedConn(session, builder)
			util.Go(func() {
				t.handleConn(fc)
			})
		}
	})

	return nil
}

// NewKCPFramedConnForRobot 是 newKCPFramedConn 的导出别名，供 robot / 测试
// 侧构造 IConn 使用（生产路径走 accept loop 内部的 newKCPFramedConn）。
// 参数 conn 必须是已连接的 net.Conn（通常是 *kcp.UDPSession）。
func NewKCPFramedConnForRobot(conn net.Conn, builder IKCPBuilder) IConn {
	return newKCPFramedConn(conn, builder)
}

// EncodeTgfBinaryFrame 是 decodeTgfBinaryFrame 的反向操作，供 robot / 测试
// 侧构造二进制帧时使用。生产 server 端编码走 TCPServer.getSendToClientData。
func EncodeTgfBinaryFrame(module, method string, data []byte) []byte {
	methodName := module + "." + method
	methodSize := uint16(len(methodName))
	dataSize := uint16(len(data))
	buf := make([]byte, 0, int(requestHeadSize)+int(methodSize)+int(dataSize))
	buf = append(buf, requestMagicNumber, byte(Logic))
	buf = append(buf, byte(methodSize>>8), byte(methodSize))
	buf = append(buf, byte(dataSize>>8), byte(dataSize))
	buf = append(buf, methodName...)
	buf = append(buf, data...)
	return buf
}

// EncodeTgfHeartbeatFrame 构造心跳帧的原始字节。
func EncodeTgfHeartbeatFrame() []byte {
	return []byte{requestMagicNumber, byte(Heartbeat)}
}
