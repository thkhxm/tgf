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

func (k *KCPServerConfig) Address() string      { return k.address }
func (k *KCPServerConfig) Port() string         { return k.port }
func (k *KCPServerConfig) ReadBufferSize() int  { return k.readBufferSize }
func (k *KCPServerConfig) WriteBufferSize() int { return k.writeBufferSize }
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
func (k *KCPServerConfig) NoDelay() int    { return k.noDelay }
func (k *KCPServerConfig) Interval() int   { return k.interval }
func (k *KCPServerConfig) Resend() int     { return k.resend }
func (k *KCPServerConfig) Nc() int         { return k.nc }
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

// WithAEADKey 设置预共享 32 字节密钥。传 nil/空切片显式表示明文模式（仅限开发/内网）。
//
// F5 fail-fast（审计 P2"AEAD 静默降级明文"）：长度非法（≠32 且 ≠0）直接 panic
// 拒绝启动——原实现静默忽略，运维传错 key（典型：64 字节 hex 字符串没解码）后
// 服务"正常"启动但全部 KCP 流量实为明文，属 fail-open 安全设计。配置错误必须
// 在启动期炸出来，而不是上线后裸奔。
func (k *KCPServerConfig) WithAEADKey(key []byte) *KCPServerConfig {
	if len(key) == 0 {
		k.aeadKey = nil
		return k
	}
	if len(key) != aeadKeySize {
		panic(fmt.Sprintf("tgf/rpc: KCP AEAD key 长度必须是 %d 字节,实际 %d 字节(若是 hex/base64 字符串请先解码;明文模式请传 nil)",
			aeadKeySize, len(key)))
	}
	k.aeadKey = key
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

// newKCPFramedConn 把一个 KCP 会话包装为 IConn。
//
// F5 fail-closed（审计 P2）：原实现 sealer 初始化失败仅 Warn 后降级 plaintextSealer
// ——配置了密钥的连接静默变明文。现在初始化失败返回 error，调用方必须关闭会话。
// F4：构造时立即设置初始 read deadline——KCP accept 后若客户端一字节不发，
// reader goroutine 原先会永久阻塞（与 WS 的同类问题一并修复）。
func newKCPFramedConn(conn net.Conn, builder IKCPBuilder) (*kcpFramedConn, error) {
	var sealer aeadSealer = plaintextSealer{}
	if key := builder.AEADKey(); len(key) > 0 {
		cs, err := newChaChaSealer(key)
		if err != nil {
			return nil, fmt.Errorf("tgf/rpc: KCP AEAD 初始化失败(fail-closed,拒绝降级明文): %w", err)
		}
		sealer = cs
	}
	if d := builder.DeadLineTime(); d > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(d))
	}
	return &kcpFramedConn{
		conn:         conn,
		sealer:       sealer,
		deadLineTime: builder.DeadLineTime(),
		writeTimeout: builder.WriteTimeout(),
		remote:       conn.RemoteAddr().String(),
	}, nil
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

func (c *kcpFramedConn) Close() error                       { return c.conn.Close() }
func (c *kcpFramedConn) RemoteAddr() string                 { return c.remote }
func (c *kcpFramedConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *kcpFramedConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

// EncodeResponse E6：KCP 走和 TCP 一样的二进制响应帧编码（不是 WS 的 WSResponse
// 协议）。原 IsWebSocket() 临时方法已随编码下沉移除。
// F5: v2 帧携带 code（限流/错误码载体）。
func (c *kcpFramedConn) EncodeResponse(messageType string, _ int32, code int32, reply []byte) []byte {
	return encodeBinaryResponseFrame(messageType, code, reply)
}

// RequiresFrameMAC F5：AEAD 已提供帧级认证加密时豁免应用层 MAC；
// 明文 KCP 与裸 TCP 同等对待，要求帧级 MAC。
func (c *kcpFramedConn) RequiresFrameMAC() bool { return !c.sealer.Enabled() }

// ReadRawFrame 读取一个完整的外层长度前缀帧并返回 AEAD Open 后的原始字节，
// 不做 tgf 帧解码。供 robot / 客户端读取服务端下行帧——下行是"响应格式"
// （robot/response_decoder.go），与 ReadFrame 解码的"请求格式"不同。
func (c *kcpFramedConn) ReadRawFrame() ([]byte, error) {
	return readKCPFrame(c.conn, c.sealer)
}

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
	case byte(Logic), byte(LogicMAC):
		if len(raw) < int(requestHeadSize) {
			return nil, fmt.Errorf("tgf/rpc: logic frame header truncated")
		}
		// 复用 encoding/binary 解析头部（和 tcpFramedConn 完全一致）
		methodSize := uint16(raw[2])<<8 | uint16(raw[3])
		dataSize := uint16(raw[4])<<8 | uint16(raw[5])
		withMAC := msgType == byte(LogicMAC)
		expected := int(requestHeadSize) + int(methodSize) + int(dataSize)
		if withMAC {
			// F5 帧级防伪：LogicMAC 在 dataSize 后插入 8B seq、帧尾追加 16B mac
			expected += frameMACSeqSize + FrameMACSize
		}
		if len(raw) < expected {
			return nil, fmt.Errorf("tgf/rpc: logic frame truncated: have %d want %d", len(raw), expected)
		}
		bodyIndex := int(requestHeadSize)
		var seq uint64
		var mac []byte
		if withMAC {
			seq = uint64(raw[bodyIndex])<<56 | uint64(raw[bodyIndex+1])<<48 |
				uint64(raw[bodyIndex+2])<<40 | uint64(raw[bodyIndex+3])<<32 |
				uint64(raw[bodyIndex+4])<<24 | uint64(raw[bodyIndex+5])<<16 |
				uint64(raw[bodyIndex+6])<<8 | uint64(raw[bodyIndex+7])
			bodyIndex += frameMACSeqSize
			mac = raw[expected-FrameMACSize : expected]
		}
		reqNameIndex := bodyIndex + int(methodSize)
		reqName := util.ConvertStringByByteSlice(raw[bodyIndex:reqNameIndex])
		ix := strings.LastIndex(reqName, ".")
		if ix < 0 {
			return nil, fmt.Errorf("tgf/rpc: malformed method name %q", reqName)
		}
		return &FrameIn{
			MessageType: Logic,
			Module:      reqName[:ix],
			Method:      reqName[ix+1:],
			Data:        raw[reqNameIndex : reqNameIndex+int(dataSize)],
			Seq:         seq,
			MAC:         mac,
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
	// F5 fail-fast：自定义 IKCPBuilder 实现可能绕过 KCPServerConfig.WithAEADKey 的
	// panic 校验——listener 启动前再兜底校验一次，密钥非法拒绝启动（fail-closed），
	// 杜绝"配了密钥却明文裸奔"。
	if key := builder.AEADKey(); len(key) != 0 && len(key) != aeadKeySize {
		return fmt.Errorf("tgf/rpc: KCP AEAD key 长度非法: %d(期望 %d 或 0=明文)——拒绝启动 KCP listener", len(key), aeadKeySize)
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
	// D 档（v3）：listener 句柄存入 TCPServer，供 CloseListeners 优雅停机关闭。
	// 注意 kcp-go 的服务端会话与 listener 共享 UDP socket，关 listener 会同时
	// 中断既有 KCP 会话（语义说明见 TCPServer.CloseListeners 注释）。
	t.listenerMu.Lock()
	t.kcpListener = listener
	t.listenerMu.Unlock()
	log.InfoTag("init", "KCP网关启动成功 addr=%s aead=%v",
		addr, len(builder.AEADKey()) == aeadKeySize)

	util.Go(func() {
		for {
			session, acceptErr := listener.AcceptKCP()
			if acceptErr != nil {
				// D 档（v3）：原实现任何 accept 错误都只打 DebugTag 即退出——
				// KCP 网关静默死亡且无告警。现在区分：停机关闭 → Info 正常退出；
				// 其他错误 → Error 级日志（kcp-go 的 AcceptKCP 错误基本只有
				// listener/socket 已关闭一类，无可重试的临时错误，故仍退出循环，
				// 但保证可观测）。
				if t.acceptClosed.Load() {
					log.InfoTag("tcp", "KCP listener 已关闭,accept 循环退出")
				} else {
					log.Error("[tcp] KCP AcceptKCP 错误,accept 循环退出 err=%v", acceptErr)
				}
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

			fc, connErr := newKCPFramedConn(session, builder)
			if connErr != nil {
				// F5 fail-closed：AEAD 初始化失败的会话直接关闭，绝不降级明文。
				log.Error("[tcp] KCP 连接初始化失败,关闭会话 addr=%v err=%v", session.RemoteAddr(), connErr)
				_ = session.Close()
				continue
			}
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
// F5：签名增加 error——AEAD 初始化失败 fail-closed，不再静默降级明文。
func NewKCPFramedConnForRobot(conn net.Conn, builder IKCPBuilder) (IConn, error) {
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
