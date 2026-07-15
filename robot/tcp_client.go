package robot

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"github.com/cornelk/hashmap"
	"github.com/gorilla/websocket"
	util2 "github.com/thkhxm/rpcx/v2/util"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/rpc"
	"github.com/thkhxm/tgf/v2/util"
	"google.golang.org/protobuf/proto"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/26
//***************************************************

type tcp struct {
	callback *hashmap.Map[string, CallbackLogic]
	buf      *bufio.Reader
	client   *net.TCPConn

	// macKey / macSeq 是帧级 MAC 状态（F5 帧级防伪，见 EnableFrameMAC）。
	// macKey 在 EnableFrameMAC 中一次性写入（登录响应回调里调用，与 Send 同属
	// 业务调用方驱动），其后 Send 只读。
	macKey []byte
	macSeq atomic.Uint64
}

// FrameMACCapable 由支持帧级 MAC（F5 帧级防伪）的 robot 客户端实现。
// 用法：登录拿到 LoginRes.ResumeToken 后调用 EnableFrameMAC(token)，其后所有
// Send/SendMessage 自动按 LogicMAC 帧（带防重放 seq + HMAC）编码。
type FrameMACCapable interface {
	EnableFrameMAC(resumeToken string)
}

func (t *tcp) Connect(address string) IRobot {
	add, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		log.InfoTag("robot", "resolve address error: %v", err)
		panic(err)
	}
	t.client, err = net.DialTCP("tcp", nil, add)
	if err != nil {
		log.InfoTag("robot", "client error: %v", err)
		panic(err)
	}
	t.buf = bufio.NewReader(t.client)
	//心跳
	util.Go(func() {
		for {
			heartbeat := make([]byte, 0, 2)
			buff := bytes.NewBuffer(heartbeat)
			buff.WriteByte(250)
			buff.WriteByte(byte(rpc.Heartbeat))
			if _, err := t.client.Write(buff.Bytes()); err != nil {
				log.InfoTag("robot", "client heartbeat write error: %v", err)
				return
			}
			log.InfoTag("robot", "client heartbeat data: %v", buff.Bytes())
			time.Sleep(time.Second * 10)
		}
	})

	//handler response
	// F5 协议修缮：原实现按 8 字节头解析且首字节==1 即当心跳丢弃——与服务端
	// 7 字节 v1 头根本不匹配（v3 审计 P1"框架自带 TCP 客户端与服务端协议互不
	// 兼容"），且无法区分心跳 0x01 与 compress=1。现在统一走 ReadServerFrame
	// （v2/v1 自适应流式解码，见 response_decoder.go）。
	util.Go(func() {
		for {
			sf, e := ReadServerFrame(t.buf)
			if e != nil {
				log.InfoTag("robot", "client response read error: %v", e)
				return
			}
			if sf.IsHeartbeat {
				log.InfoTag("robot", "收到服务器响应的心跳包")
				continue
			}
			if sf.IsReplaceKick {
				log.InfoTag("robot", "收到服务器的替换登录通知(账号在别处登录)")
				continue
			}
			if f, has := t.callback.Get(sf.MessageType); has {
				f(t, sf.Data)
			}
			log.InfoTag("robot", "收到服务器的响应数据 messageType:%v code:%v 数据:%v",
				sf.MessageType, sf.Code, util.ConvertStringByByteSlice(sf.Data))
		}
	})
	//
	return t
}

// EnableFrameMAC 启用帧级 MAC（F5）：登录拿到 LoginRes.ResumeToken 后调用，
// 其后 Send/SendMessage 自动按 LogicMAC 帧（带防重放 seq + HMAC-SHA256 截断）
// 编码。与服务端 rpc.DeriveFrameMACKey 使用同一派生算法。
func (t *tcp) EnableFrameMAC(resumeToken string) {
	t.macKey = rpc.DeriveFrameMACKey(resumeToken)
	t.macSeq.Store(0)
	log.InfoTag("robot", "帧级MAC已启用")
}

func (t *tcp) RegisterCallbackMessage(messageType string, f CallbackLogic) IRobot {
	t.callback.Insert(messageType, f)
	return t
}

func (t *tcp) Send(messageType string, v1 proto.Message) {
	data, err := proto.Marshal(v1)
	if err != nil {
		log.Warn("robot Send: 编码请求失败 messageType=%s err=%v", messageType, err)
		return
	}
	// F5: 启用帧级 MAC 后按 LogicMAC 帧编码（防重放 seq 严格递增）。
	if t.macKey != nil {
		ix := strings.LastIndex(messageType, ".")
		if ix < 0 {
			log.Warn("robot Send: messageType 格式必须是 module.method, 实际 %s", messageType)
			return
		}
		frame := rpc.EncodeTgfBinaryMACFrame(messageType[:ix], messageType[ix+1:], data, t.macSeq.Add(1), t.macKey)
		if _, err := t.client.Write(frame); err != nil {
			log.Warn("robot Send: 发送 MAC 请求失败 messageType=%s err=%v", messageType, err)
			return
		}
		log.InfoTag("robot", "发送MAC请求 messageType:%v len:%v", messageType, len(frame))
		return
	}
	reqName := []byte(messageType)
	tmp := make([]byte, 0, 6+len(data)+len(reqName))
	buff := bytes.NewBuffer(tmp)
	buff.WriteByte(250)
	buff.WriteByte(byte(rpc.Logic))
	reqLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqLenByte, uint16(len(reqName)))
	buff.Write(reqLenByte)
	reqSizeLenByte := make([]byte, 2)
	binary.BigEndian.PutUint16(reqSizeLenByte, uint16(len(data)))
	buff.Write(reqSizeLenByte)
	buff.Write(reqName)
	buff.Write(data)
	if _, err := t.client.Write(buff.Bytes()); err != nil {
		log.Warn("robot Send: 发送请求失败 messageType=%s err=%v", messageType, err)
		return
	}
	log.InfoTag("robot", "发送请求 messageType:%v 数据:%v", messageType, buff.Bytes())
}

func (t *tcp) SendMessage(module, serviceName string, v1 proto.Message) {
	ms := module + "." + serviceName
	t.Send(ms, v1)
}

type ws struct {
	path          string
	conn          *websocket.Conn
	heartbeatData []byte
	closeChan     chan struct{}
	sendChan      chan message
	callback      *hashmap.Map[string, CallbackLogic]
	start         time.Time
}
type wss struct {
	ws
}
type message struct {
	messageType int
	data        []byte
}

var costCache = make([]*atomic.Int64, 6)
var lastReq, curReq = &atomic.Int64{}, &atomic.Int64{}

func (w *ws) Connect(address string) IRobot {
	u := url.URL{Scheme: "ws", Host: address, Path: w.path}
	//log.Info("连接到 %s", u.String())

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Info("连接失败:%v", err)
		panic(err)
	}
	w.heartbeatData = []byte{byte(1)}
	w.closeChan = make(chan struct{})
	w.sendChan = make(chan message, 100)
	w.conn = conn
	//监听关闭事件
	w.conn.SetCloseHandler(func(code int, text string) error {
		w.closeChan <- struct{}{}
		return nil
	})

	w.conn.SetPongHandler(func(appData string) error {
		//	收到服务器的pong响应
		//log.DebugTag("tcp", "收到服务器的pong响应 data=%v", appData)
		return nil
	})

	// 启动读取协程，处理从服务器接收到的消息
	util.Go(func() {
		defer func() {
			if err := w.conn.Close(); err != nil {
				log.Info("关闭 WebSocket 连接失败:%v", err)
			}
		}()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Info("读取消息失败:%v", err)
				return
			}
			cost := time.Since(w.start).Milliseconds()
			index := getCostIndex(cost)
			costCache[index].Add(1)
			curReq.Add(1)
			res := &rpc.WSResponse{}
			if unmarshalErr := proto.Unmarshal(message, res); unmarshalErr != nil {
				log.Info("解析 WebSocket 响应失败:%v", unmarshalErr)
				continue
			}
			//log.Info("收到消息: %s", res.MessageType)
			if f, has := w.callback.Get(res.MessageType); has {
				data := res.GetData()
				if res.Zip {
					if data, err = util2.Unzip(res.GetData()); err != nil {
						log.Info("解压数据失败:%v", err)
					}
				}
				f(w, data)
			}
		}
	})

	//启动心跳
	util.Go(func() {
		for {
			select {
			//监听关闭信号
			case <-w.closeChan:
				log.InfoTag("tcp", "连接断开,停止心跳发送")
				return
			default:
				w.sendChan <- message{websocket.PingMessage, w.heartbeatData}
				//log.InfoTag("tcp", "心跳发送")
			}
			time.Sleep(time.Second * 5)
		}
	})

	util.Go(func() {
		for send := range w.sendChan {
			if err := w.conn.WriteMessage(send.messageType, send.data); err != nil {
				log.Info("发送 WebSocket 消息失败:%v", err)
				return
			}
		}
	})
	return w
}
func (w *wss) Connect(address string) IRobot {
	u := url.URL{Scheme: "wss", Host: address, Path: w.path}
	log.Info("连接到 %s", u.String())

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Info("连接失败:%v", err)
		panic(err)
	}
	w.heartbeatData = []byte{byte(1)}
	w.closeChan = make(chan struct{})
	w.sendChan = make(chan message, 100)
	w.conn = conn
	//监听关闭事件
	w.conn.SetCloseHandler(func(code int, text string) error {
		w.closeChan <- struct{}{}
		return nil
	})

	w.conn.SetPongHandler(func(appData string) error {
		//	收到服务器的pong响应
		//log.DebugTag("tcp", "收到服务器的pong响应 data=%v", appData)
		return nil
	})

	// 启动读取协程，处理从服务器接收到的消息
	util.Go(func() {
		defer func() {
			if err := w.conn.Close(); err != nil {
				log.Info("关闭 WebSocket 连接失败:%v", err)
			}
		}()
		for {
			_, message, err := conn.ReadMessage()
			cost := time.Since(w.start).Milliseconds()
			if cost > 500 {
				log.Info("消息处理时间:%v", cost)
			}
			if err != nil {
				log.Info("读取消息失败:%v", err)
				return
			}
			res := &rpc.WSResponse{}
			if unmarshalErr := proto.Unmarshal(message, res); unmarshalErr != nil {
				log.Info("解析 WebSocket 响应失败:%v", unmarshalErr)
				continue
			}
			//log.Info("收到消息: %s", res.MessageType)
			if f, has := w.callback.Get(res.MessageType); has {
				data := res.GetData()
				if res.Zip {
					if data, err = util2.Unzip(res.GetData()); err != nil {
						log.Info("解压数据失败:%v", err)
					}
				}
				f(w, data)
			}
		}
	})

	//启动心跳
	util.Go(func() {
		for {
			select {
			//监听关闭信号
			case <-w.closeChan:
				log.InfoTag("tcp", "连接断开,停止心跳发送")
				return
			default:
				w.sendChan <- message{websocket.PingMessage, w.heartbeatData}
				//log.InfoTag("tcp", "心跳发送")
			}
			time.Sleep(time.Second * 5)
		}
	})

	util.Go(func() {
		for send := range w.sendChan {
			if err := w.conn.WriteMessage(send.messageType, send.data); err != nil {
				log.Info("发送 WebSocket 消息失败:%v", err)
				return
			}
		}
	})
	return w
}

func (w *ws) RegisterCallbackMessage(messageType string, f CallbackLogic) IRobot {
	w.callback.Insert(messageType, f)
	return w
}

func (w *ws) Send(messageType string, v1 proto.Message) {
	ms := strings.Split(messageType, ".")
	//time.Sleep(time.Millisecond * 300)
	w.SendMessage(ms[0], ms[1], v1)
	w.start = time.Now()
}

func (w *ws) SendMessage(module, serviceName string, v1 proto.Message) {
	data, err := proto.Marshal(v1)
	if err != nil {
		log.Info("编码 WebSocket 请求失败:%v", err)
		return
	}
	m := &rpc.WSMessage{
		Module:      module,
		ServiceName: serviceName,
		Data:        data,
	}
	md, err := proto.Marshal(m)
	if err != nil {
		log.Info("编码 WebSocket 消息失败:%v", err)
		return
	}
	w.sendChan <- message{websocket.BinaryMessage, md}
	//err := w.conn.WriteMessage(websocket.BinaryMessage, md)
	//if err != nil {
	//	log.Info("发送消息失败:%v", err)
	//	return
	//}
}

func NewRobotTcp() IRobot {
	t := &tcp{}
	t.callback = hashmap.New[string, CallbackLogic]()
	return t
}

func NewRobotWs(path string) IRobot {
	t := &ws{}
	if path[0:1] != "/" {
		path = "/" + path
	}
	t.path = path
	t.callback = hashmap.New[string, CallbackLogic]()
	return t
}

func NewRobotWss(path string) IRobot {
	t := &wss{}
	if path[0:1] != "/" {
		path = "/" + path
	}
	t.path = path
	t.callback = hashmap.New[string, CallbackLogic]()
	return t
}

func init() {
	costCache[0] = &atomic.Int64{}
	costCache[1] = &atomic.Int64{}
	costCache[2] = &atomic.Int64{}
	costCache[3] = &atomic.Int64{}
	costCache[4] = &atomic.Int64{}
	costCache[5] = &atomic.Int64{}
	go func() {
		t := time.NewTimer(time.Second)
		for {
			<-t.C
			qps := curReq.Load() - lastReq.Load()
			lastReq.Store(curReq.Load())
			log.InfoTag("tcp", "消息处理时间统计:qps:%v, 0-50ms:%v, 50-100ms:%v, 100-300ms:%v, 300-600ms:%v, 600-1000ms:%v, >1000ms:%v",
				qps, costCache[0].Load(), costCache[1].Load(), costCache[2].Load(), costCache[3].Load(), costCache[4].Load(), costCache[5].Load())
			t.Reset(time.Second)
		}
	}()
}

func getCostIndex(mill int64) int32 {
	if mill < 50 {
		return 0
	} else if mill < 100 {
		return 1
	} else if mill < 300 {
		return 2
	} else if mill < 600 {
		return 3
	} else if mill < 1000 {
		return 4
	} else {
		return 5
	}
}
