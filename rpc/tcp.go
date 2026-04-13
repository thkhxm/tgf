package rpc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cornelk/hashmap"
	"github.com/gorilla/websocket"
	"github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/share"
	util2 "github.com/smallnest/rpcx/util"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/metrics"
	"github.com/thkhxm/tgf/rpc/internal"
	"github.com/thkhxm/tgf/util"
	"github.com/valyala/bytebufferpool"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/28
//***************************************************

// RequestHeader
// [1][1][2][2][n][n]
// magic number|message type|request method name size|data size|method name|data
type RequestHeader []byte

// ResponseHeader
// [1][1][2][4][n][n]
// message type|compress|request method name size|data size|method name|data
type ResponseHeader []byte

type HeaderMessageType byte

// B4 埋点：网关活跃连接数。
// 通过包级 var 延迟到首次使用时再问 Provider 要 Gauge——避免包 init 阶段
// 固定到启动时的 Noop Provider，让 WithMetrics(...) 在 Run 之前切换 Provider
// 的场景仍然有效。使用 sync.Once 保证跨 goroutine 幂等。
var (
	gateConnGaugeOnce sync.Once
	gateConnGauge     metrics.Gauge
)

func getGateConnGauge() metrics.Gauge {
	gateConnGaugeOnce.Do(func() {
		gateConnGauge = metrics.NewGauge("tgf_gate_connections", "网关当前活跃连接数")
	})
	return gateConnGauge
}

// resetGateConnGaugeOnceForTest 只用于单测，允许切换 Provider 后重新绑定 gauge。
func resetGateConnGaugeOnceForTest() {
	gateConnGaugeOnce = sync.Once{}
	gateConnGauge = nil
}

var upGrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024 * 8,
}

type Args[T protoreflect.ProtoMessage] struct {
	ByteData []byte
}

func (a *Args[T]) GetData() (res T) {
	var ()
	v := reflect.ValueOf(res)
	if v.Kind() == reflect.Interface && v.IsNil() {
		v = reflect.New(v.Type().Elem())
	}
	return util.ConvertToPB[T](a.ByteData)
}

type Reply[T protoreflect.ProtoMessage] struct {
	ByteData []byte
	Code     int32
}

func (r *Reply[T]) SetData(data T) (err error) {
	var ()
	r.ByteData, err = proto.Marshal(data)
	return
}

func (r *Reply[T]) SetCode(code int32) {
	r.Code = code
}

var (

	// 请求头长度
	requestHeadSize uint16 = 6
	// 请求登录头长度
	requestLoginHeadSize uint16 = 4
	// 最大同时连接数
	maxSynChanConn = 3000
	// 连接超时时间

	// 协议魔法值,避免恶意请求
	requestMagicNumber byte = 250
	//最低压缩大小
	compressMinSize = 1024 * 2

	loginTokenTimeOut = time.Hour * 12

	heartbeatData    = []byte{byte(Heartbeat)}
	replaceLoginData = []byte{byte(ReplaceLogin)}
	wsUpGrader       = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024 * 8,
		CheckOrigin:     checkOrigin,
	}
)

const (
	Heartbeat HeaderMessageType = iota + 1
	Logic
	ReplaceLogin
)

const (
	netTcp = iota
	netWebsocket
)

const (
	defaultTcpServerPort = "8230"
	//读缓冲区大小
	defaultReadBuffer = 1024
	//写缓冲区大小
	defaultWriteBuffer = 8 * 1024
	//默认tcp监听的地址
	defaultIp             = "0.0.0.0"
	defaultMaxConnections = 10000
)

// 网关连接的几个 deadline 默认值——v2 改为 var + init 从环境变量读取，
// 业务可通过 EnvironmentTCPDeadLineSec / EnvironmentTCPWriteTimeoutMs /
// EnvironmentTCPSendChanTimeoutMs 在启动时覆盖。零配置时仍然是历史默认值。
var (
	defaultDeadLineTime    = time.Second * 60 // 读 idle 超时
	defaultWriteDeadline   = time.Second * 5  // A2-phase3: WriteFrame 前的 write deadline
	defaultSendChanTimeout = time.Second * 3  // Send 推入 writeChan 的最大等待
)

func init() {
	// v2: 从环境变量加载网关连接默认超时。
	// 直接用 tgf.GetStrConfig——init 顺序：tgf.InitConfig (init.go) 先于
	// rpc 包的 init，所以 mapping 已经就绪。
	if v := tgf.GetStrConfig[int](tgf.EnvironmentTCPDeadLineSec); v > 0 {
		defaultDeadLineTime = time.Duration(v) * time.Second
	}
	if v := tgf.GetStrConfig[int](tgf.EnvironmentTCPWriteTimeoutMs); v > 0 {
		defaultWriteDeadline = time.Duration(v) * time.Millisecond
	}
	if v := tgf.GetStrConfig[int](tgf.EnvironmentTCPSendChanTimeoutMs); v > 0 {
		defaultSendChanTimeout = time.Duration(v) * time.Millisecond
	}
}

type IUserConnectData interface {
	UpdateUserNodeId(servicePath, nodeId string)
	GetContextData() *share.Context
	GetChannel() chan *client.Call
	Offline(replace bool)
	// Send 把 data 推入 writeChan 交给 writer goroutine 异步写出。
	// A2-phase3: 签名从 `Send(data)` 改为 `Send(data) error`，返回值语义：
	//   - nil：已入队
	//   - tgf.ErrConnClosed：连接已 Offline
	//   - tgf.ErrConnSendTimeout：writeChan 满且 defaultSendChanTimeout 仍未被消费；
	//     此时 Send 会触发 Offline 清理链路。
	Send(data []byte) error
	IsLogin() bool
	Login(userId string)
	Stop()
}

type ITCPService interface {
	Run()
	UpdateUserNodeInfo(userId, servicePath, nodeId string) bool
	// ToUser 推送消息到指定 userId 的连接。
	// A2-phase3: 签名从 `... bool` 改为 `... error`，返回值语义：
	//   - nil：连接存在且已入队
	//   - tgf.ErrUserNotFound：目标用户在本节点不在线
	//   - tgf.ErrConnClosed / tgf.ErrConnSendTimeout：Send 层错误透传
	ToUser(userId, messageType string, data []byte) error
	DoLogin(userId, templateUserId string) (err error)

	Offline(userId string, replace bool) (exists bool)
}

type ITCPBuilder interface {
	WithPort(port string) ITCPBuilder
	WithBuffer(readBuffer, writeBuffer int) ITCPBuilder
	WithWSPath(path string) ITCPBuilder
	Address() string
	Port() string
	WsPath() string
	MaxConnections() int32
	// DeadLineTime 是连接的 read idle deadline。每收到一帧会重置一次。
	DeadLineTime() time.Duration
	// WriteTimeout 是底层 conn.WriteFrame 调用时设置的 write deadline。
	// A2-phase3 新增；未被 WithWriteTimeout 覆盖时返回 defaultWriteDeadline。
	WriteTimeout() time.Duration
	WithWriteTimeout(d time.Duration) ITCPBuilder
	ReadBufferSize() int
	WriteBufferSize() int
	IsWebSocket() bool
	WithWss(certFile, keyFile string) ITCPBuilder
	IsWss() bool
	WssCertFile() string
	WssKeyFile() string
}

type TCPServer struct {
	config  ITCPBuilder       //tcp连接配置
	conChan chan *net.TCPConn //客户端连接chan

	closeChan chan bool //关闭chan
	users     *hashmap.Map[string, IUserConnectData]
	//
	startup *sync.Once //是否已经启动
}

type ServerConfig struct {
	address         string //地址
	port            string //端口
	wsPath          string //wsPath
	maxConnections  int32  //最大连接数
	deadLineTime    time.Duration
	writeTimeout    time.Duration // A2-phase3: 写入级超时；0 表示用默认值
	readBufferSize  int
	writeBufferSize int
	netType         int
	isWss           bool

	wSSKeyPath  string
	wSSCertPath string
	//

}

func (s *ServerConfig) Address() string {
	return s.address
}

func (s *ServerConfig) Port() string {
	return s.port
}
func (s *ServerConfig) WsPath() string {
	return s.wsPath
}
func (s *ServerConfig) MaxConnections() int32 {
	return s.maxConnections
}

func (s *ServerConfig) DeadLineTime() time.Duration {
	return s.deadLineTime
}

func (s *ServerConfig) WriteTimeout() time.Duration {
	if s.writeTimeout <= 0 {
		return defaultWriteDeadline
	}
	return s.writeTimeout
}

func (s *ServerConfig) WithWriteTimeout(d time.Duration) ITCPBuilder {
	if d > 0 {
		s.writeTimeout = d
	}
	return s
}

func (s *ServerConfig) ReadBufferSize() int {
	return s.readBufferSize
}

func (s *ServerConfig) WriteBufferSize() int {
	return s.writeBufferSize
}
func (s *ServerConfig) IsWebSocket() bool {
	return s.netType == netWebsocket
}
func (s *ServerConfig) WithPort(port string) ITCPBuilder {
	var ()
	s.port = port
	s.netType = netTcp
	return s
}
func (s *ServerConfig) WithWSPath(path string) ITCPBuilder {
	var ()
	if path[0:1] == "/" {
		path = path[1:]
	}
	s.wsPath = path
	s.netType = netWebsocket
	return s
}
func (s *ServerConfig) WithBuffer(readBuffer, writeBuffer int) ITCPBuilder {
	var ()
	s.readBufferSize = readBuffer
	s.writeBufferSize = writeBuffer
	return s
}

func (s *ServerConfig) WithWss(certFile, keyFile string) ITCPBuilder {
	var ()
	s.isWss = true
	s.wSSCertPath = certFile
	s.wSSKeyPath = keyFile
	return s
}

func (s *ServerConfig) IsWss() bool {
	return s.isWss
}

func (s *ServerConfig) WssCertFile() string {
	return s.wSSCertPath
}

func (s *ServerConfig) WssKeyFile() string {
	return s.wSSKeyPath
}

// sessionState 是单个连接的生命周期状态。
// A3-phase1 用 atomic CAS 建立状态机，替代 A2-phase1 引入的 offlineOnce。
//
// 状态流转（正常路径）：
//
//	Idle ──tryBeginLogin──> LoggingIn ──markOnline──> Online
//	  │              │                    │
//	  └──────────────┴────────────────────┴──> Offlining（任一状态 CAS → Offlining，唯一成功者负责清理）
//
// 禁止的非法流转（由 CAS 自然阻止）：
//   - Offlining → 任何其他状态（Offlining 是终态）
//   - Online → LoggingIn（不允许重入登录流程）
type sessionState int32

const (
	sessionIdle      sessionState = iota // 连接刚建立，尚未 DoLogin
	sessionLoggingIn                     // DoLogin 正在处理
	sessionOnline                        // 登录完成，接收业务请求
	sessionOfflining                     // 下线清理中或已结束
)

type UserConnectData struct {
	// A2-phase2: conn 是统一的连接抽象，替代原先的 `*net.TCPConn` + `*websocket.Conn`
	// 双字段 + nil 分支。对于测试场景允许 conn 为 nil（phase1 的单测就是这么跑的）。
	conn        IConn
	reqCount    int32
	userId      string
	contextData *share.Context
	reqChan     chan *client.Call
	// stop 是关闭信号：Offline 通过 CAS 保证只有一个 goroutine 关闭它。
	// 所有监听该通道的 goroutine（logic / writer / reader 的 select 分支）
	// 收到后各自退出。任何地方都不要再向 stop 发送值。
	stop      chan struct{}
	writeChan chan []byte

	// state 是 sessionState 的原子持有者。A3-phase1 用它替代 A2-phase1 的 offlineOnce：
	// "任一状态 → Offlining" 的 CAS 恰好成功一次，承担 Offline 清理幂等保证；
	// 同时 "Idle → LoggingIn → Online" 的 CAS 阻止"DoLogin 到一半被 Offline"的边界情况。
	state atomic.Int32

	// replace 仅作为"本次下线是否是被替换登录踢掉"的语义标记，由 Offline 入参写入，
	// 供 OfflineHook 传播用。只会在 CAS 进入 Offlining 的瞬间被赋值一次。
	replace bool
}

// loadState / casState 是 sessionState 的低层访问器。
func (u *UserConnectData) loadState() sessionState {
	return sessionState(u.state.Load())
}

// tryBeginLogin 尝试 Idle → LoggingIn。失败说明连接不在 Idle（可能被别的
// 登录路径抢了，或者已经 Offline）。
func (u *UserConnectData) tryBeginLogin() bool {
	return u.state.CompareAndSwap(int32(sessionIdle), int32(sessionLoggingIn))
}

// markOnline 尝试 LoggingIn → Online。失败通常意味着连接在 DoLogin 期间被强制 Offline。
func (u *UserConnectData) markOnline() bool {
	return u.state.CompareAndSwap(int32(sessionLoggingIn), int32(sessionOnline))
}

// tryBeginOfflining 尝试把任意非 Offlining 状态迁到 Offlining。
// 恰好一个 goroutine 拿到 true，它负责执行真正的清理（关 stop、关 conn、发 OfflineHook）。
// 其他并发调用者拿到 false，直接 return。
func (u *UserConnectData) tryBeginOfflining() bool {
	for {
		cur := u.state.Load()
		if sessionState(cur) == sessionOfflining {
			return false
		}
		if u.state.CompareAndSwap(cur, int32(sessionOfflining)) {
			return true
		}
		// CAS 失败：有并发者改了状态，重新读再试。
	}
}

// isActiveForLogic 表示连接处于可接收业务请求的状态。
// Idle 和 Online 都允许——Idle 是因为登录 RPC 本身也走 doLogic 路径。
// LoggingIn/Offlining 都拒绝，避免"登录 RPC 还没返回客户端又发别的"或"关闭途中仍处理请求"。
func (u *UserConnectData) isActiveForLogic() bool {
	s := u.loadState()
	return s == sessionIdle || s == sessionOnline
}

type RequestData struct {
	User          *UserConnectData
	RequestMethod string
	Module        string
	Data          []byte
	MessageType   HeaderMessageType
	ReqId         int32
	StartTime     time.Time
}

func (t *TCPServer) selectorChan() {
	for {
		select {
		case con := <-t.conChan:
			c := con
			util.Go(func() {
				// phase2: 裸 TCP 连接先包成 IConn，再走统一 handleConn。
				// phase3: 额外传入 writeTimeout（WriteFrame 前的 SetWriteDeadline 值）。
				fc := newTCPFramedConn(c, t.config.ReadBufferSize(), t.config.WriteTimeout())
				t.handleConn(fc)
			})
		}
	}
}

func (t *TCPServer) onDestroy() {
	t.closeChan <- true
}

func checkOrigin(r *http.Request) bool {
	var ()
	return true
}

// handleConn 是 A2-phase2 合流后的唯一连接处理入口。
// 不管底层是裸 TCP 还是 WebSocket，accept / upgrade 之后都统一包成 IConn 走这里。
//
// 为什么不叫 handlerConn：原来的 handlerConn/handlerWSConn 是两套互斥的大函数，
// phase2 把它们合并后改名为 handleConn，避免与旧函数名混淆——如果有业务代码 grep
// 老函数名也能立刻发现 API 变了（phase1 的 `Stop()` 语义变化同理）。
func (t *TCPServer) handleConn(conn IConn) {
	connectData := &UserConnectData{
		conn:        conn,
		reqCount:    0,
		contextData: share.NewContext(context.Background()),
		reqChan:     make(chan *client.Call, 1), // 限制用户的请求处于并行状态
		stop:        make(chan struct{}),
		writeChan:   make(chan []byte, 20),
	}

	reqMetaData := make(map[string]string)
	templateUserId := util.GenerateSnowflakeId()
	reqMetaData[tgf.ContextKeyTemplateUserId] = templateUserId
	// WS 原路径会把 gate 的本地地址写进 meta（便于跨节点路由），TCP 原路径不写。
	// phase2 里统一让 WS 仍写入、TCP 仍不写入——行为与 phase1 完全一致。
	if conn.IsWebSocket() {
		reqMetaData[tgf.GatewayServiceModuleName] = internal.LocalServerAddress
	}
	connectData.contextData.SetValue(share.ReqMetaDataKey, reqMetaData)
	t.users.Set(templateUserId, connectData)
	log.DebugTag("tcp", "接收到一条新的连接 addr=%v templateUserId=%v ws=%v",
		conn.RemoteAddr(), templateUserId, conn.IsWebSocket())

	// B4 埋点：活跃连接数 gauge +1；defer -1 保证任何退出路径都配对。
	gauge := getGateConnGauge()
	gauge.Inc()

	// 统一的 reqChan 缓冲策略：原来 WS=10 / TCP=0 两套，phase2 统一为 16（平衡吞吐与背压）。
	reqChan := make(chan *RequestData, 16)

	defer func() {
		if err := recover(); err != nil {
			log.DebugTag("tcp", "连接异常关闭 %v", err)
		}
		// 避免并发情况下，新登录用户数据比旧连接清理先执行
		if tmpUser, ok := t.users.Get(connectData.userId); ok {
			if GetTemplateUserId(tmpUser.GetContextData()) == GetTemplateUserId(connectData.GetContextData()) {
				t.users.Del(connectData.userId)
			}
		}
		connectData.Offline(false)
		gauge.Dec()
	}()

	// logic goroutine
	util.Go(func() {
		for {
			select {
			case req, ok := <-reqChan:
				if !ok {
					return
				}
				t.doLogic(req)
			case <-connectData.stop:
				return
			}
		}
	})

	// writer goroutine
	util.Go(func() {
		connectData.writeMessage()
	})

	// reader 主循环：ReadFrame 阻塞读一帧，分发给 logic goroutine。
	// 退出路径：ReadFrame 返回 error（连接被 Offline 关闭 / 客户端断开 / 解码失败）。
	for {
		frame, err := conn.ReadFrame()
		if err != nil {
			if err != io.EOF {
				log.DebugTag("tcp", "ReadFrame error user=%v err=%v", connectData.userId, err)
			}
			return
		}
		// phase3: idle deadline 统一管理——每收到一帧就把 read deadline 重置为 now + idle。
		// wsFramedConn 自己的 Ping/Pong handler 也会调 SetReadDeadline 走同一路径。
		_ = conn.SetReadDeadline(time.Now().Add(t.config.DeadLineTime()))

		switch frame.MessageType {
		case Heartbeat:
			// 只有 TCP 路径会走到这里（WS 心跳已由 wsFramedConn 的 Ping/Pong handler 原生处理）。
			// phase2 走 Send 走 writer goroutine 统一出口，不再直接 conn.Write——避免和 writer 并发写。
			// phase3: Send 返回 error，心跳路径忽略（连接异常由 Send 内部触发 Offline）。
			_ = connectData.Send(heartbeatData)

		case Logic:
			pack := &RequestData{
				RequestMethod: frame.Method,
				Module:        frame.Module,
				Data:          frame.Data,
				User:          connectData,
				ReqId:         frame.ReqId,
			}
			log.DebugTag("tcp", "收到请求[%s.%s]", pack.Module, pack.RequestMethod)
			select {
			case reqChan <- pack:
			case <-connectData.stop:
				return
			default:
				log.DebugTag("tcp", "用户[%s] 请求处理繁忙,直接丢弃请求[%s.%s]",
					connectData.userId, pack.Module, pack.RequestMethod)
			}
			connectData.reqCount++

		default:
			log.DebugTag("tcp", "收到未知消息类型 user=%v type=%v", connectData.userId, frame.MessageType)
		}
	}
}

func (t *TCPServer) Offline(userId string, replace bool) (exists bool) {
	oldUser, _ := t.users.Get(userId)
	if oldUser != nil {
		defer func() {
			if err := recover(); err != nil {
				log.WarnTag("tcp", "关闭用户连接异常 %v", err)
				exists = true
			}
		}()
		if replace {
			// 发送重复登录消息通知——Send 返回 error 在这里被忽略，
			// 因为接下来就要调用 oldUser.Offline(replace) 主动断开，连接命运已定。
			_ = oldUser.Send(replaceLoginData)
			time.Sleep(time.Second * 1)
		}
		//断开已经在线的玩家上下文
		oldUser.Offline(replace)
		exists = true
		log.InfoTag("login", "重复登录,踢掉在线玩家 userId=%v", userId)
	}
	return
}

func (t *TCPServer) DoLogin(userId, templateUserId string) error {
	userData, _ := t.users.Get(templateUserId)
	if userData == nil {
		return errors.New("用户不存在")
	}
	// A3-phase1: 登录入口必须能 CAS 到 LoggingIn——意味着连接处于 Idle 状态，
	// 没有被其他 Login / Offline 抢占。否则拒绝登录。
	u, ok := userData.(*UserConnectData)
	if !ok {
		return errors.New("用户连接类型异常")
	}
	if !u.tryBeginLogin() {
		return fmt.Errorf("连接状态异常,无法登录 state=%d", u.loadState())
	}

	ct := u.GetContextData()
	ct.SetReqMetaData(tgf.ContextKeyUserId, userId)
	t.users.Set(userId, u)
	u.Login(userId)

	// A3-phase1: LoggingIn → Online 的 CAS 收尾。
	// 失败通常意味着 Login 调用过程中另一个路径（例如 ToUser panic、Send 超时）
	// 已经把连接标记为 Offlining——这种情况下我们回滚 users 表条目并返回错误。
	if !u.markOnline() {
		t.users.Del(userId)
		return fmt.Errorf("登录过程中连接被中止 state=%d", u.loadState())
	}

	// 成功后清理 templateUserId 的索引条目
	t.users.Del(templateUserId)
	log.InfoTag("tcp", "login templateUserId %v , uuid %v", templateUserId, userId)
	return nil
}

func (t *TCPServer) doLogic(data *RequestData) {
	// A3-phase1: 只对处于 Idle/Online 状态的连接处理请求。Offlining 状态可能
	// 是 logic goroutine 还没退出但 Offline 已经触发，拒绝请求避免对已死连接
	// 做无意义的下游调用。LoggingIn 状态拒绝是为了避免登录过程中客户端抢发业务请求。
	if !data.User.isActiveForLogic() {
		log.DebugTag("tcp", "drop request on inactive connection user=%v state=%d module=%v method=%v",
			data.User.userId, data.User.loadState(), data.Module, data.RequestMethod)
		return
	}

	var (
		err         error
		messageType = data.Module + "." + data.RequestMethod
	)
	data.StartTime = time.Now()
	reply := make([]byte, 0)

	reqData := &Args[protoreflect.ProtoMessage]{}
	reqData.ByteData = data.Data

	resData := &Reply[protoreflect.ProtoMessage]{}
	data.User.StartReq()
	defer func() {
		consumeTime := time.Since(data.StartTime).Milliseconds()
		if consumeTime > 100 {
			log.WarnTag("tcp", "用户[%s] 慢请求耗时统计 module=%v serviceName=%v consumeTime=%v", data.User.userId, data.Module, data.RequestMethod, consumeTime)
		}
		//记录客户端请求日志
		log.Service(data.Module, data.RequestMethod, "1.0",
			data.User.userId,
			consumeTime, resData.Code)
	}()
	err = sendMessage(data.User, data.Module, data.RequestMethod, reqData, resData)
	if err != nil {
		log.InfoTag("tcp", "请求异常 module=%s serviceName=%s 数据 [%v]", data.Module, data.RequestMethod, err)
		return
	}
	//callbackErr := callback.Done()
	//if callbackErr != nil {
	//	log.InfoTag("tcp", "请求异常 数据 [%v] [%v]", data, callbackErr)
	//	return
	//}
	reply = resData.ByteData
	clientData := t.getSendToClientData(messageType, data.ReqId, resData.Code, reply)
	// phase3: Send 返回 error 时记录日志——连接此时要么正在 Offline，
	// 要么 writer 已卡死由 Send 内部触发 Offline，无需额外处理。
	if sendErr := data.User.Send(clientData); sendErr != nil {
		log.DebugTag("tcp", "doLogic 响应推送失败 user=%v module=%v method=%v err=%v",
			data.User.userId, data.Module, data.RequestMethod, sendErr)
	}
}

func (t *TCPServer) getSendToClientData(messageType string, reqId, code int32, reply []byte) (res []byte) {
	var (
		compress byte = 0
		err      error
	)

	//逻辑响应
	if t.config.IsWebSocket() {
		data := &WSResponse{}
		data.MessageType = messageType
		if len(reply) > compressMinSize {
			reply, err = util2.Zip(reply)
			data.Zip = true
		}

		data.Data = reply
		data.ReqId = reqId
		data.Code = code
		//b, _ := proto.Marshal(data)
		//bp.Write(b)
		res, _ = proto.Marshal(data)
		return
	}

	// [1][1][2][4][n][n]
	// message type|compress|request method name size|data size|method name|data
	bp := bytebufferpool.Get()
	//放回池子
	defer bytebufferpool.Put(bp)

	if len(reply) >= compressMinSize {
		compress = 1
	}

	//是否压缩
	bp.WriteByte(compress)
	//压缩数据
	if compress == 1 {
		reply, err = util2.Zip(reply)
		if err != nil {
			log.WarnTag("tcp", "数据压缩异常 压缩数据 [%v] [%v]", reply, err)
			return
		}
	}

	//响应函数长度
	mtSize := len(messageType)
	rqBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(rqBytes, uint16(mtSize))
	bp.Write(rqBytes)
	//响应内容长度
	dataBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(dataBytes, uint32(len(reply)))
	bp.Write(dataBytes)
	//响应函数名
	bp.WriteString(messageType)
	//响应内容
	bp.Write(reply)

	//输出最终bytes数据
	res = bp.Bytes()
	return
}

func (t *TCPServer) Update() {
	var ()
}

// PostCall
// @Description: 执行完业务之后的处理切片
// @receiver this
// @param ctx
// @param servicePath
// @param serviceMethod
// @param args
// @param reply
// @param err
// @return error

func (t *TCPServer) Run() {
	var ()
	//保证每个tcp只会被启动一次,避免误操作
	t.startup.Do(func() {
		if t.config.IsWebSocket() {
			util.Go(func() {
				log.InfoTag("init", "启动ws服务 %v", t.config.Address()+":"+t.config.Port()+"/"+t.config.WsPath())
				// 定义 WebSocket 路由
				http.HandleFunc("/"+t.config.WsPath(), t.wsHandler)
				// 启动服务器
				var err error
				if t.config.IsWss() {
					err = http.ListenAndServeTLS(t.config.Address()+":"+t.config.Port(), t.config.WssCertFile(), t.config.WssKeyFile(), nil)
				} else {
					err = http.ListenAndServe(t.config.Address()+":"+t.config.Port(), nil)
				}

				if err != nil {
					log.Info("服务器启动失败：%v", err)
					return
				}
			})
		} else {
			//
			add, _ := net.ResolveTCPAddr("tcp", fmt.Sprintf("%v:%v", t.config.Address(), t.config.Port()))
			listen, err := net.ListenTCP("tcp", add)
			if err != nil {
				log.DebugTag("init", "tcp服务 启动异常 %v", err)
				return
			}
			log.InfoTag("init", "tcp服务 启动成功 %v", add)

			//启动selector线程，等待连接接入
			util.Go(func() {
				log.InfoTag("init", "tcp selector 启动成功")
				t.selectorChan()
			})

			util.Go(func() {
				log.InfoTag("init", "tcp 开始监听连接")
				for {
					tcp, _ := listen.AcceptTCP()
					tcp.SetNoDelay(true)                           //无延迟
					tcp.SetKeepAlive(true)                         //保持激活
					tcp.SetReadBuffer(t.config.ReadBufferSize())   //设置读缓冲区大小
					tcp.SetWriteBuffer(t.config.WriteBufferSize()) //设置写缓冲区大小
					tcp.SetDeadline(time.Now().Add(t.config.DeadLineTime()))
					t.conChan <- tcp //将链接放入管道中
				}
			})
		}

	})
}

func (t *TCPServer) wsHandler(w http.ResponseWriter, r *http.Request) {
	// phase2: 升级为 WS 后立即包装成 IConn，随后和 TCP 路径一样走 handleConn。
	// phase3: 把 writeTimeout 一并传入，替代原先 10 分钟的魔数。
	fc, err := wsFramedConnFromRequest(w, r, t.config.DeadLineTime(), t.config.WriteTimeout())
	if err != nil {
		log.Info("%v", err)
		return
	}
	util.Go(func() {
		t.handleConn(fc)
	})
}

func (t *TCPServer) UpdateUserNodeInfo(userId, servicePath, nodeId string) bool {
	var (
		res = false
	)
	if connectData, ok := t.users.Get(userId); ok {
		connectData.UpdateUserNodeId(servicePath, nodeId)
		res = true
	}
	return res
}

// ToUser 从网关推送一条消息到指定 userId。
// A2-phase3: 返回 error 而非 bool，语义见 ITCPService 接口文档。
func (t *TCPServer) ToUser(userId, messageType string, data []byte) (err error) {
	// phase1 修复：原代码 recover 后只做 t.users.Del(userId)，连接 goroutine
	// 并不会退出，下一次调用 Send 还会再 panic；现在 panic 后走 Offline 清理全链路。
	defer func() {
		if r := recover(); r != nil {
			log.WarnTag("tcp", "ToUser panic user=%v msg=%v err=%v", userId, messageType, r)
			if u, exists := t.users.Get(userId); exists {
				u.Offline(false)
			}
			t.users.Del(userId)
			err = fmt.Errorf("tgf/rpc: ToUser panic: %v", r)
		}
	}()
	connectData, ok := t.users.Get(userId)
	if !ok {
		log.DebugTag("tcp", "userid=%v user connection not found", userId)
		return tgf.ErrUserNotFound
	}
	res := t.getSendToClientData(messageType, 0, 0, data)
	return connectData.Send(res)
}

func (u *UserConnectData) UpdateUserNodeId(servicePath, nodeId string) {
	var ()
	u.contextData.SetReqMetaData(servicePath, nodeId)
}
func (u *UserConnectData) IsLogin() bool {
	var ()
	return u.userId != ""
}

func (u *UserConnectData) GetContextData() *share.Context {
	var ()
	return u.contextData
}
func (u *UserConnectData) GetChannel() chan *client.Call {
	var ()
	return u.reqChan
}
// Offline 是下线清理的唯一入口。
// A2-phase1 用 sync.Once 保证幂等；A3-phase1 把幂等语义提升到完整的 sessionState 状态机：
// "任一状态 → Offlining" 的 CAS 恰好成功一次，同时防御"DoLogin 到一半被强制下线"
// 的边界情况——那种情况下 DoLogin 后续的 markOnline 会 CAS 失败并回滚。
//
// 历史修复（A2-phase1）：
//   - 原代码用 `if u.replace { return }` 做去重，非原子；
//   - Offline 里 `u.stop <- struct{}{}` 发送值，但 logic goroutine 会 close(stop)，
//     两者叠加触发 "send on closed channel" panic；
//   - 原代码有 `u.contextData.Deadline()` 一行死代码，已删除。
func (u *UserConnectData) Offline(replace bool) {
	// CAS 失败说明连接已经在 Offlining 或已结束。直接 return，不做任何事。
	// 恰好一个 goroutine 会 CAS 成功并走下面的清理路径。
	if !u.tryBeginOfflining() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.DebugTag("tcp", "用户 userId=%v Offline panic: %v", u.userId, r)
		}
	}()
	u.replace = replace

	for _, key := range u.contextData.GetAllReqMetaDataKeys() {
		SendRPCMessageByStr(u.contextData, key, "OfflineHook",
			&OfflineReq{UserId: u.userId, Replace: replace}, &EmptyReply{})
	}

	// 关闭底层连接，让 reader 主循环里阻塞在 ReadFrame 的调用立即返回 error。
	// phase2: 原先 TCP/WS 两套 nil 判断简化为单一 IConn 分支。
	ip := ""
	if u.conn != nil {
		ip = u.conn.RemoteAddr()
		_ = u.conn.Close()
	}

	// 通过 close 通知所有监听 stop 的 goroutine（logic / writer）退出。
	// 这里 close 是安全的：CAS 保证了本函数体恰好执行一次。
	close(u.stop)

	log.DebugTag("tcp", "用户 userId=%v 离线 ip=%v replace=%v", u.userId, ip, replace)
}

// Stop 以前是向 stop 通道发送一个值，会与 Offline 的 close(stop) 叠加 panic。
// 现在统一走 Offline 路径——外部没有调用方（phase1 审计过），留着作为 legacy 入口。
func (u *UserConnectData) Stop() {
	u.Offline(false)
}
func (u *UserConnectData) Login(userId string) {
	defer func() {
		if r := recover(); r != nil {
			log.DebugTag("tcp", "用户 userId=%v Login: %v", u.userId, r)
		}
	}()
	var (
		err error
	)
	u.userId = userId
	for _, key := range u.contextData.GetAllReqMetaDataKeys() {
		err = SendRPCMessageByStr(u.contextData, key, "LoginHook", &DefaultArgs{C: u.userId}, &EmptyReply{})
		if err != nil && !errors.Is(err, tgf.ServiceNotFound) {
			log.WarnTag("tcp", "用户 userId=%s LoginHook: %v", u.userId, err)
		}
	}
}
// Send 把 data 推入 writeChan 交给 writer goroutine 异步写出。
// A2-phase3 签名变化：返回 error 而非静默丢弃。
//   - writeChan 有空位 → 立即入队，返回 nil
//   - u.stop 已关闭（连接已 Offline）→ 直接返回 tgf.ErrConnClosed，不尝试入队
//   - writeChan 满 + defaultSendChanTimeout 仍未被 writer 消费
//     → 判定为连接异常（writer goroutine 很可能已经卡在底层 conn.Write），
//     调用 Offline(false) 触发清理，返回 tgf.ErrConnSendTimeout
//
// 调用方的错误处理策略：
//   - 业务响应推送（doLogic）→ 记录日志即可，连接即将被清理
//   - 心跳 / ReplaceLogin 通知 → 忽略 error（连接已在关闭路径）
//   - ToUser（外部推送入口）→ 把 error 返回给调用方
func (u *UserConnectData) Send(data []byte) error {
	// 快速路径：先检查连接是否已关闭，避免对一个已死连接做 3 秒超时等待。
	select {
	case <-u.stop:
		return tgf.ErrConnClosed
	default:
	}
	select {
	case u.writeChan <- data:
		return nil
	case <-u.stop:
		return tgf.ErrConnClosed
	case <-time.After(defaultSendChanTimeout):
		log.WarnTag("tcp", "用户 %s writeChan 推入超时，判定连接异常，触发 Offline", u.userId)
		u.Offline(false)
		return tgf.ErrConnSendTimeout
	}
}

func (u *UserConnectData) StartReq() {
	u.contextData.SetReqMetaData(tgf.ContextKeyTRACEID, util.GenerateSnowflakeId())
}

func (u *UserConnectData) writeMessage() {
	defer func() {
		if e := recover(); e != nil {
			log.WarnTag("tcp", "发送请求异常 user=%v err=%v", u.userId, e)
		}
	}()
	for {
		select {
		case d, open := <-u.writeChan:
			if !open {
				return
			}
			if u.conn == nil {
				log.DebugTag("tcp", "用户没有可用的连接数据 %v", u.userId)
				return
			}
			// phase2: WS 和 TCP 都走 conn.WriteFrame，IConn 实现自己处理
			// write deadline、帧封装等细节。
			if err := u.conn.WriteFrame(d); err != nil {
				log.DebugTag("tcp", "WriteFrame 失败 user=%v err=%v", u.userId, err)
				return
			}
		case <-u.stop:
			// writeChan 不再由任何人 close——writer 的退出路径是监听 stop。
			return
		}
	}
}

func newTCPBuilder() ITCPBuilder {
	serverConfig := &ServerConfig{
		address:         defaultIp,
		port:            defaultTcpServerPort,
		readBufferSize:  defaultReadBuffer,
		writeBufferSize: defaultWriteBuffer,
		deadLineTime:    defaultDeadLineTime,
		maxConnections:  defaultMaxConnections,
	}
	return serverConfig
}

func newDefaultTCPServer(builder ITCPBuilder) *TCPServer {
	server := &TCPServer{}
	server.config = builder
	server.conChan = make(chan *net.TCPConn, maxSynChanConn)
	server.closeChan = make(chan bool, 1)
	server.startup = new(sync.Once)
	server.users = hashmap.New[string, IUserConnectData]()
	return server
}
