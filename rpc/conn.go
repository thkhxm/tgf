package rpc

// A2-phase2 引入的统一连接抽象。
//
// 原先 tcp.go 里 UserConnectData 同时持有 `*net.TCPConn` 和 `*websocket.Conn`
// 两个字段，所有读写/关闭/下线逻辑都要两套分支；handlerConn 和 handlerWSConn
// 也是两套 ~150 行的 reader 循环。phase2 把这些都收敛到 IConn 之后，UserConnectData
// 只持有 IConn，handleConn 只有一条路径。
//
// IConn 同时是 A8（UDP/KCP 网关）的扩展点——届时新增一个 kcpFramedConn 实现即可，
// 不用再改 handleConn/UserConnectData 的代码。

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"google.golang.org/protobuf/proto"
)

// FrameIn 是从连接上读到的一帧解码后结构，屏蔽 TCP/WS/未来 KCP 的差异。
type FrameIn struct {
	MessageType HeaderMessageType
	Module      string
	Method      string
	Data        []byte
	// ReqId 仅 WebSocket 路径有值（客户端请求 id），TCP 路径填 0。
	ReqId int32
}

// IConn 是网关连接的抽象。read/write 粒度是"一帧"，由实现负责分帧与解码。
// 所有方法都应当是 goroutine-safe-读写分离：ReadFrame 在 reader goroutine 调用，
// WriteFrame 在 writer goroutine 调用，Close/SetDeadline 可在任意 goroutine。
type IConn interface {
	ReadFrame() (*FrameIn, error)
	WriteFrame(data []byte) error
	Close() error
	RemoteAddr() string
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	// IsWebSocket 标记连接类型，getSendToClientData 当前还依赖它选择编码格式。
	// phase3/C1 把编码也下沉到 conn 适配器后这个方法可以删掉。
	IsWebSocket() bool
}

// ---- TCP 适配器 ----

// tcpFramedConn 字段类型是 net.Conn 而非 *net.TCPConn——这样 net.Pipe、
// TLS wrapper、测试 mock 都能复用。生产路径传入的仍然是 *net.TCPConn。
type tcpFramedConn struct {
	conn         net.Conn
	reader       *bufio.Reader
	writeTimeout time.Duration
}

// newTCPFramedConn 构造一个 TCP 帧适配器。
// A2-phase3: writeTimeout 作为每次 WriteFrame 前 SetWriteDeadline 的值；
// 传 0 时 fallback 到 defaultWriteDeadline。
func newTCPFramedConn(c net.Conn, readBuf int, writeTimeout time.Duration) *tcpFramedConn {
	if readBuf <= 0 {
		readBuf = defaultReadBuffer
	}
	if writeTimeout <= 0 {
		writeTimeout = defaultWriteDeadline
	}
	return &tcpFramedConn{
		conn:         c,
		reader:       bufio.NewReaderSize(c, readBuf),
		writeTimeout: writeTimeout,
	}
}

// ReadFrame 按 tgf 自有二进制协议读一帧：
//
//	[1:magic][1:msgType] { [2:methodSize][2:dataSize][n:method][n:data] }
//
// Heartbeat 帧只有前两个字节；Logic 帧有完整头和负载。
func (c *tcpFramedConn) ReadFrame() (*FrameIn, error) {
	head, err := c.reader.Peek(2)
	if err != nil {
		return nil, err
	}
	if head[0] != requestMagicNumber {
		return nil, fmt.Errorf("tcp frame magic mismatch: got %v want %v", head[0], requestMagicNumber)
	}
	msgType := head[1]
	switch msgType {
	case byte(Heartbeat):
		if _, err := c.reader.Discard(2); err != nil {
			return nil, err
		}
		return &FrameIn{MessageType: Heartbeat}, nil

	case byte(Logic):
		head, err = c.reader.Peek(int(requestHeadSize))
		if err != nil {
			return nil, err
		}
		methodSize := binary.BigEndian.Uint16(head[2:4])
		dataSize := binary.BigEndian.Uint16(head[4:6])
		totalLen := int(requestHeadSize) + int(methodSize) + int(dataSize)
		all := make([]byte, totalLen)
		if _, err := io.ReadFull(c.reader, all); err != nil {
			return nil, err
		}
		reqNameIndex := int(requestHeadSize) + int(methodSize)
		reqName := util.ConvertStringByByteSlice(all[int(requestHeadSize):reqNameIndex])
		ix := strings.LastIndex(reqName, ".")
		if ix < 0 {
			return nil, fmt.Errorf("tcp frame malformed method name: %q", reqName)
		}
		return &FrameIn{
			MessageType: Logic,
			Module:      reqName[:ix],
			Method:      reqName[ix+1:],
			Data:        all[reqNameIndex:],
		}, nil

	default:
		return nil, fmt.Errorf("tcp frame unknown message type: %d", msgType)
	}
}

func (c *tcpFramedConn) WriteFrame(data []byte) error {
	// phase3: 每次写前设置 write deadline，避免对端接收慢时整条 writer goroutine 卡住。
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	_, err := c.conn.Write(data)
	return err
}

func (c *tcpFramedConn) Close() error                        { return c.conn.Close() }
func (c *tcpFramedConn) RemoteAddr() string                  { return c.conn.RemoteAddr().String() }
func (c *tcpFramedConn) SetReadDeadline(t time.Time) error   { return c.conn.SetReadDeadline(t) }
func (c *tcpFramedConn) SetWriteDeadline(t time.Time) error  { return c.conn.SetWriteDeadline(t) }
func (c *tcpFramedConn) IsWebSocket() bool                   { return false }

// ---- WebSocket 适配器 ----

type wsFramedConn struct {
	conn         *websocket.Conn
	deadLineTime time.Duration
	writeTimeout time.Duration
}

// newWSFramedConn 包装一个已 upgrade 完毕的 websocket.Conn，并在构造时注册
// ping/pong/close 的原生 handler（原先散落在 handlerWSConn 里的逻辑收敛到这里）。
// A2-phase3: writeTimeout 用于每次 WriteFrame 前的 SetWriteDeadline，
// 替代原先写死的 10 分钟魔数。
func newWSFramedConn(c *websocket.Conn, deadLineTime, writeTimeout time.Duration) *wsFramedConn {
	if writeTimeout <= 0 {
		writeTimeout = defaultWriteDeadline
	}
	w := &wsFramedConn{conn: c, deadLineTime: deadLineTime, writeTimeout: writeTimeout}
	c.SetPingHandler(func(msg string) error {
		err := c.WriteControl(websocket.PongMessage, []byte(msg), time.Now().Add(deadLineTime))
		if err == websocket.ErrCloseSent {
			return nil
		}
		if e, ok := err.(net.Error); ok && e.Timeout() {
			return nil
		}
		_ = c.SetReadDeadline(time.Now().Add(deadLineTime * 2))
		return err
	})
	c.SetPongHandler(func(string) error {
		_ = c.SetReadDeadline(time.Now().Add(deadLineTime * 2))
		return nil
	})
	c.SetCloseHandler(func(code int, text string) error {
		log.DebugTag("tcp", "收到客户端的主动关闭连接消息 code=%v text=%v", code, text)
		return nil
	})
	return w
}

// ReadFrame 阻塞读一条 WebSocket 消息，解码为 FrameIn。非 BinaryMessage 的其他
// 类型会继续循环等下一条。PingMessage/PongMessage 的心跳已由原生 handler 处理，
// 这里理论上不会收到（ReadMessage 内部就过滤了），保留 continue 作为防御。
func (w *wsFramedConn) ReadFrame() (*FrameIn, error) {
	for {
		msgType, message, err := w.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		switch msgType {
		case websocket.BinaryMessage:
			data := &WSMessage{}
			if err := proto.Unmarshal(message, data); err != nil {
				return nil, err
			}
			_ = w.conn.SetReadDeadline(time.Now().Add(w.deadLineTime))
			return &FrameIn{
				MessageType: Logic,
				Module:      data.Module,
				Method:      data.ServiceName,
				Data:        data.Data,
				ReqId:       data.ReqId,
			}, nil
		default:
			// 非 binary 帧（如服务端错误收到 text）——丢弃继续读
			continue
		}
	}
}

func (w *wsFramedConn) WriteFrame(data []byte) error {
	// phase3: 使用构造时注入的 writeTimeout，默认 5s。原代码是写死的 10 分钟。
	_ = w.conn.SetWriteDeadline(time.Now().Add(w.writeTimeout))
	return w.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (w *wsFramedConn) Close() error                        { return w.conn.Close() }
func (w *wsFramedConn) RemoteAddr() string                  { return w.conn.RemoteAddr().String() }
func (w *wsFramedConn) SetReadDeadline(t time.Time) error   { return w.conn.SetReadDeadline(t) }
func (w *wsFramedConn) SetWriteDeadline(t time.Time) error  { return w.conn.SetWriteDeadline(t) }
func (w *wsFramedConn) IsWebSocket() bool                   { return true }

// ---- HTTP upgrade helper ----
//
// wsFramedConnFromRequest 给 TCPServer.wsHandler 在 HTTP 路由里用：一行完成
// upgrade 并包装成 IConn，避免在 wsHandler 里再写 upgrader 样板。
func wsFramedConnFromRequest(w http.ResponseWriter, r *http.Request, deadLineTime, writeTimeout time.Duration) (IConn, error) {
	conn, err := wsUpGrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	return newWSFramedConn(conn, deadLineTime, writeTimeout), nil
}
