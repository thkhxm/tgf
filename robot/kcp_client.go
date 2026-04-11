package robot

// A8: 最小化的 KCP 机器人客户端，供联调和单测使用。
// 不追求功能完备——它只负责：
//   - dial KCP server（接受预共享 AEAD 密钥）
//   - 发送一帧 Logic 消息
//   - 阻塞读一帧响应
//
// 真正给线上业务用的客户端应当支持心跳、重连、trace id 等，那是 robot 的
// tcp_client 已有的职责——如果需要 KCP 版本可以复制扩展。

import (
	"context"
	"fmt"
	"time"

	"github.com/thkhxm/tgf/rpc"
	"github.com/xtaci/kcp-go"
)

// KCPClient 是一个最小化的 KCP 客户端。
type KCPClient struct {
	session *kcp.UDPSession
	conn    rpc.IConn // rpc 包提供的适配器——我们通过一个对外暴露的工厂构造
}

// DialKCP 用和 NewKCPBuilder 对齐的默认参数拨号 server。aeadKey 必须和 server
// 端配置的一致（长度 32）；传 nil 走明文模式。
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
	c := &KCPClient{
		session: session,
		conn:    rpc.NewKCPFramedConnForRobot(session, builder),
	}
	_ = ctx // 预留 context 取消；当前实现用 session deadline 代替
	return c, nil
}

// SendLogic 构造一个 Logic 帧并发出。
func (c *KCPClient) SendLogic(module, method string, data []byte) error {
	raw := rpc.EncodeTgfBinaryFrame(module, method, data)
	_ = c.session.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.conn.WriteFrame(raw)
}

// SendHeartbeat 发一帧心跳。
func (c *KCPClient) SendHeartbeat() error {
	return c.conn.WriteFrame(rpc.EncodeTgfHeartbeatFrame())
}

// ReadFrame 阻塞读一帧。
func (c *KCPClient) ReadFrame() (*rpc.FrameIn, error) {
	_ = c.session.SetReadDeadline(time.Now().Add(5 * time.Second))
	return c.conn.ReadFrame()
}

// Close 关闭底层 KCP session。
func (c *KCPClient) Close() error { return c.session.Close() }
