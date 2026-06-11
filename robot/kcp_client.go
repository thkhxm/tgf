package robot

// A8: 最小化的 KCP 机器人客户端，供联调和单测使用。
// 不追求功能完备——它只负责：
//   - dial KCP server（接受预共享 AEAD 密钥）
//   - 发送一帧 Logic 消息（F5 起支持帧级 MAC）
//   - 阻塞读一帧响应（请求格式 ReadFrame / 下行响应格式 ReadServerFrame）
//
// 真正给线上业务用的客户端应当支持心跳、重连、trace id 等，那是 robot 的
// tcp_client 已有的职责——如果需要 KCP 版本可以复制扩展。

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/thkhxm/tgf/v2/rpc"
	"github.com/xtaci/kcp-go"
)

// KCPClient 是一个最小化的 KCP 客户端。
type KCPClient struct {
	session *kcp.UDPSession
	conn    rpc.IConn // rpc 包提供的适配器——我们通过一个对外暴露的工厂构造

	// macKey / macSeq 帧级 MAC 状态（F5，见 EnableFrameMAC）。
	macKey []byte
	macSeq atomic.Uint64
}

// rawFrameReader 投影 rpc 包 KCP 适配器的 ReadRawFrame 能力（读取下行响应格式
// 的原始帧字节，不做请求格式解码）。
type rawFrameReader interface {
	ReadRawFrame() ([]byte, error)
}

// DialKCP 用和 NewKCPBuilder 对齐的默认参数拨号 server。aeadKey 必须和 server
// 端配置的一致（长度 32）；传 nil 走明文模式。
// F5: 密钥长度非法不再被静默忽略——rpc.NewKCPBuilder.WithAEADKey 会 panic，
// rpc.NewKCPFramedConnForRobot 初始化失败会返回 error（fail-closed）。
func DialKCP(ctx context.Context, addr string, aeadKey []byte) (*KCPClient, error) {
	// 为保证和 rpc 包内部使用的 UDPSession 参数一致，这里走默认的 NoDelay=1/10/2/1。
	session, err := kcp.DialWithOptions(addr, nil, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("kcp dial %s: %w", addr, err)
	}
	session.SetNoDelay(1, 10, 2, 1)
	session.SetStreamMode(true)
	session.SetACKNoDelay(true)

	builder := rpc.NewKCPBuilder("0")
	if len(aeadKey) > 0 {
		builder = builder.WithAEADKey(aeadKey)
	}
	conn, err := rpc.NewKCPFramedConnForRobot(session, builder)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("kcp conn init: %w", err)
	}
	c := &KCPClient{
		session: session,
		conn:    conn,
	}
	_ = ctx // 预留 context 取消；当前实现用 session deadline 代替
	return c, nil
}

// EnableFrameMAC 启用帧级 MAC（F5）：登录拿到 LoginRes.ResumeToken 后调用，
// 其后 SendLogic 自动按 LogicMAC 帧编码。
// 注意：KCP+AEAD 通道服务端豁免 MAC（传输层已鉴真），明文 KCP 才需要。
func (c *KCPClient) EnableFrameMAC(resumeToken string) {
	c.macKey = rpc.DeriveFrameMACKey(resumeToken)
	c.macSeq.Store(0)
}

// SendLogic 构造一个 Logic 帧并发出（启用 MAC 后为 LogicMAC 帧）。
func (c *KCPClient) SendLogic(module, method string, data []byte) error {
	var raw []byte
	if c.macKey != nil {
		raw = rpc.EncodeTgfBinaryMACFrame(module, method, data, c.macSeq.Add(1), c.macKey)
	} else {
		raw = rpc.EncodeTgfBinaryFrame(module, method, data)
	}
	_ = c.session.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.conn.WriteFrame(raw)
}

// SendHeartbeat 发一帧心跳。
func (c *KCPClient) SendHeartbeat() error {
	return c.conn.WriteFrame(rpc.EncodeTgfHeartbeatFrame())
}

// ReadFrame 阻塞读一帧并按**请求格式**解码。
// 注意：服务端的下行帧是"响应格式"（与请求格式不同），正常下行消费请用
// ReadServerFrame；本方法保留给回显/联调场景。
func (c *KCPClient) ReadFrame() (*rpc.FrameIn, error) {
	_ = c.session.SetReadDeadline(time.Now().Add(5 * time.Second))
	return c.conn.ReadFrame()
}

// ReadServerFrame 阻塞读一帧服务端下行并按**响应格式**解码（v2/v1 自适应，
// 含心跳/替换登录通知/逻辑响应+code）。F5 修复：原 kcpRobot 用请求格式解码
// 服务端响应，格式不匹配时帧已被消费只能丢弃（v3 审计已指出）。
func (c *KCPClient) ReadServerFrame() (*ServerFrame, error) {
	rr, ok := c.conn.(rawFrameReader)
	if !ok {
		return nil, errors.New("robot: kcp conn 不支持 ReadRawFrame")
	}
	_ = c.session.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, err := rr.ReadRawFrame()
	if err != nil {
		return nil, err
	}
	return DecodeServerFrame(raw)
}

// Close 关闭底层 KCP session。
func (c *KCPClient) Close() error { return c.session.Close() }
