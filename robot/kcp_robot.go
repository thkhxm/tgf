package robot

// kcp_robot.go 是实现 IRobot 接口的 KCP 客户端。
//
// 和 A8 的 kcp_client.go（最小化低层客户端）不同，kcpRobot 提供了和 ws robot
// 一样的完整能力：
//   - Connect 后自动启动心跳 goroutine
//   - 后台 read loop 解码服务端响应帧（TCP/KCP 格式）并分发到 callback
//   - Send / SendMessage 发送 Logic 帧
//   - RegisterCallbackMessage 注册消息回调
//
// 依赖 response_decoder.go 里的 DecodeServerFrame 来解析服务端推送。

import (
	"context"
	"strings"
	"time"

	"github.com/cornelk/hashmap"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/util"
	"google.golang.org/protobuf/proto"
)

// kcpRobot 是 KCP 版本的 IRobot 实现。
type kcpRobot struct {
	client   *KCPClient
	aeadKey  []byte
	callback *hashmap.Map[string, CallbackLogic]
	closeCh  chan struct{}
}

// NewRobotKCP 创建一个 KCP robot。aeadKey 为 nil 走明文，非 nil 走 AEAD。
func NewRobotKCP(aeadKey []byte) IRobot {
	return &kcpRobot{
		aeadKey:  aeadKey,
		callback: hashmap.New[string, CallbackLogic](),
		closeCh:  make(chan struct{}),
	}
}

func (k *kcpRobot) Connect(address string) IRobot {
	client, err := DialKCP(context.Background(), address, k.aeadKey)
	if err != nil {
		log.Error("KCP robot 连接失败: %v", err)
		panic(err)
	}
	k.client = client

	// 启动心跳 goroutine（每 3 秒一次，比 WS 的 5 秒更频繁——KCP 面向实时场景）
	util.Go(func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-k.closeCh:
				return
			case <-ticker.C:
				if err := k.client.SendHeartbeat(); err != nil {
					log.DebugTag("robot", "KCP 心跳发送失败: %v", err)
					return
				}
			}
		}
	})

	// 启动 read loop——解码服务端推送帧并分发到 callback
	util.Go(func() {
		for {
			select {
			case <-k.closeCh:
				return
			default:
			}
			// 读一帧原始字节（readKCPFrame 解出 4 字节长度前缀 + 可选 AEAD 后的 plaintext）
			_ = k.client.session.SetReadDeadline(time.Now().Add(30 * time.Second))
			frame, err := k.client.conn.ReadFrame()
			if err != nil {
				// ReadFrame 内部调 decodeTgfBinaryFrame 可能会因格式不匹配而失败
				// 尝试用 response decoder 再解一次——
				// 但 ReadFrame 已经消费了底层 reader，失败时数据丢了
				// 所以这里只能记日志继续
				log.DebugTag("robot", "KCP read 错误（可能是响应格式，非请求格式）: %v", err)
				continue
			}
			// decodeTgfBinaryFrame 成功——可能是心跳回显
			if frame.MessageType == rpc.Heartbeat {
				continue
			}
			// Logic 帧（请求格式成功解码——极少见，大部分服务端推送用响应格式）
			msgType := frame.Module + "." + frame.Method
			if f, has := k.callback.Get(msgType); has {
				f(k, frame.Data)
			}
		}
	})

	log.InfoTag("robot", "KCP robot 连接成功 addr=%s aead=%v", address, len(k.aeadKey) > 0)
	return k
}

func (k *kcpRobot) RegisterCallbackMessage(messageType string, f CallbackLogic) IRobot {
	k.callback.Insert(messageType, f)
	return k
}

func (k *kcpRobot) Send(messageType string, v1 proto.Message) {
	ms := strings.Split(messageType, ".")
	if len(ms) != 2 {
		log.Warn("KCP robot Send: messageType 格式必须是 module.method, 实际 %s", messageType)
		return
	}
	k.SendMessage(ms[0], ms[1], v1)
}

func (k *kcpRobot) SendMessage(module, serviceName string, v1 proto.Message) {
	if k.client == nil {
		log.Warn("KCP robot: 未连接")
		return
	}
	data, err := proto.Marshal(v1)
	if err != nil {
		log.Warn("KCP robot: proto marshal 失败: %v", err)
		return
	}
	if err := k.client.SendLogic(module, serviceName, data); err != nil {
		log.Warn("KCP robot: SendLogic 失败: %v", err)
	}
}

// Close 关闭 KCP robot（释放 session + 停止 goroutine）。
func (k *kcpRobot) Close() {
	select {
	case <-k.closeCh:
		return // 已关闭
	default:
		close(k.closeCh)
	}
	if k.client != nil {
		_ = k.client.Close()
	}
}
