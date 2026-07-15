package rpc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"context"

	"github.com/cornelk/hashmap"
	"github.com/gorilla/websocket"
	"github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/rpcx/v2/share"
	util2 "github.com/thkhxm/rpcx/v2/util"
	"github.com/thkhxm/tgf/v2"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/metrics"
	"github.com/thkhxm/tgf/v2/rpc/internal"
	"github.com/thkhxm/tgf/v2/util"
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
//
// F5 文档化（审计"64KB 单帧上限文档化或解除"）：请求帧的 data size 字段是 uint16，
// 单帧业务负载上限 65535 字节（method name 同理）。这是协议设计上的硬上限——
// 客户端超过 64KB 的上行数据需要业务层自行分片。下行（响应）帧的 data size 是
// uint32，无此限制（KCP 通道另有 1MB 防 DoS 帧上限，见 frame_crypto.go）。
//
// F5 帧级防伪：message type = LogicMAC(4) 时帧尾追加 [8B seq][...][16B mac]，
// 完整格式见 frame_mac.go。
type RequestHeader []byte

// ResponseHeader
//
// F5 协议修缮（v2，默认）：所有 TCP/KCP 下行帧统一带 [magic][message type] 两字节
// 引导头，消除"心跳响应 0x01 与压缩响应首字节 compress=1 在字节流上不可区分"的
// 设计歧义（审计 P1）：
//
//	心跳:        [251][1]
//	逻辑响应:    [251][2][1:compress][4:code][2:method size][4:data size][n:method][n:data]
//	替换登录通知: [251][3]
//
// v1（legacy，兼容窗口，UseLegacyResponseProtocol(true) 切换）：
//
//	心跳:     [1]
//	逻辑响应: [1:compress][2:method size][4:data size][n:method][n:data]（无 code 字段）
//	替换登录: [3]
//
// v2 相对 v1 额外携带 int32 业务 code——网关的显式限流响应（F4，CodeGateBusy）
// 依赖该字段才能被 TCP/KCP 客户端感知。
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

// ---- F4/F5 过载保护与协议防伪埋点 ----
//
//   - tgf_gate_connections_rejected_total：MaxConnections 超限被拒绝的连接数（F4）
//   - tgf_gate_mac_reject_total：帧级 MAC 校验失败被断开的连接数（F5）
var (
	gateOverloadMetricsOnce sync.Once
	gateConnRejectedTotal   metrics.Counter
	gateMACRejectTotal      metrics.Counter
)

func ensureGateOverloadMetrics() {
	gateOverloadMetricsOnce.Do(func() {
		gateConnRejectedTotal = metrics.NewCounter(
			"tgf_gate_connections_rejected_total",
			"网关因连接数超过 MaxConnections 而拒绝的连接总数",
		)
		gateMACRejectTotal = metrics.NewCounter(
			"tgf_gate_mac_reject_total",
			"网关因帧级 MAC 校验失败(伪造/重放/缺失)而断开的连接总数",
		)
	})
}

func incGateConnRejected() {
	ensureGateOverloadMetrics()
	gateConnRejectedTotal.Inc()
}

func incGateMACReject() {
	ensureGateOverloadMetrics()
	gateMACRejectTotal.Inc()
}

// resetGateOverloadMetricsOnceForTest 只用于单测：切换 Provider 后重新绑定。
func resetGateOverloadMetricsOnceForTest() {
	gateOverloadMetricsOnce = sync.Once{}
	gateConnRejectedTotal = nil
	gateMACRejectTotal = nil
}

type Args[T protoreflect.ProtoMessage] struct {
	ByteData []byte
}

func (a *Args[T]) GetData() T {
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

// E6 死代码清理（v3 审计 P3）：删除三个声明后零引用的僵尸量——
// upGrader（wsUpGrader 的无 CheckOrigin 重复定义）、requestLoginHeadSize、
// loginTokenTimeOut。closeChan/onDestroy 等已在 D 档优雅停机改造时移除。
var (

	// 请求头长度
	requestHeadSize uint16 = 6
	// 最大同时连接数
	maxSynChanConn = 3000
	// 连接超时时间

	// 协议魔法值,避免恶意请求
	requestMagicNumber byte = 250
	// F5: v2 下行（响应）帧的引导字节。与请求 magic(250)、legacy 心跳(0x01)、
	// legacy compress 位(0x00/0x01) 均不冲突——客户端读首字节即可无歧义判定协议版本。
	responseMagicNumber byte = 251
	//最低压缩大小
	compressMinSize = 1024 * 2

	// heartbeatData / replaceLoginData 是 v2 协议（默认）的心跳响应与替换登录通知帧。
	// legacy v1 的对应帧见 legacyHeartbeatData / legacyReplaceLoginData；
	// 发送路径统一走 heartbeatResponseFrame() / replaceLoginNotifyFrame() 按协议版本选择。
	heartbeatData          = []byte{responseMagicNumber, byte(Heartbeat)}
	replaceLoginData       = []byte{responseMagicNumber, byte(ReplaceLogin)}
	legacyHeartbeatData    = []byte{byte(Heartbeat)}
	legacyReplaceLoginData = []byte{byte(ReplaceLogin)}

	wsUpGrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024 * 8,
		CheckOrigin:     checkOrigin,
	}
)

const (
	Heartbeat HeaderMessageType = iota + 1
	Logic
	ReplaceLogin
	// LogicMAC 是带帧级 MAC 的业务请求（F5 帧级防伪，客户端→服务端方向）。
	// 帧格式 = Logic 帧在 [2:data size] 之后插入 [8B seq]，帧尾追加 [16B mac]，
	// 详见 frame_mac.go。语义上等同 Logic——解码后 FrameIn.MessageType 仍为 Logic，
	// 以 FrameIn.MAC != nil 区分。
	LogicMAC
)

// ---- F5: 响应协议版本开关（进程级） ----
//
// v2（默认）：所有 TCP/KCP 下行帧带 [responseMagicNumber][messageType] 引导头，
// Logic 响应额外携带 int32 code（格式见 ResponseHeader 注释）。
// v1（legacy）：v2 之前的旧帧格式，供"服务端先升级、存量客户端后升级"的兼容窗口：
// 业务在启动时调用 UseLegacyResponseProtocol(true)，待客户端全量升级后移除。
// 注意 legacy 协议保留审计指出的设计歧义（心跳 0x01 vs compress=1）且无 code 字段
// ——F4 的显式限流响应在 legacy 模式下对 TCP/KCP 客户端退化为空响应帧。
var legacyResponseProtocol atomic.Bool

// UseLegacyResponseProtocol 切换 TCP/KCP 下行帧协议版本。enable=true 使用 v1 旧帧
// （兼容窗口），false（默认）使用 v2。robot 包的解码器同时兼容两个版本。
func UseLegacyResponseProtocol(enable bool) {
	legacyResponseProtocol.Store(enable)
	if enable {
		log.WarnTag("init", "TCP/KCP 下行协议切换为 legacy v1(兼容窗口)——心跳/压缩首字节歧义与无 code 字段的限制随之保留,客户端升级后请移除该开关")
	}
}

// responseProtocolIsV2 返回当前下行帧协议是否 v2（默认 true）。
func responseProtocolIsV2() bool { return !legacyResponseProtocol.Load() }

// heartbeatResponseFrame 返回当前协议版本下的心跳响应帧。
func heartbeatResponseFrame() []byte {
	if responseProtocolIsV2() {
		return heartbeatData
	}
	return legacyHeartbeatData
}

// replaceLoginNotifyFrame 返回当前协议版本下的替换登录（顶号）通知帧。
func replaceLoginNotifyFrame() []byte {
	if responseProtocolIsV2() {
		return replaceLoginData
	}
	return legacyReplaceLoginData
}

// CodeGateBusy 是网关过载（reqChan 背压满 / 连接尚不可处理请求）时回写给客户端的
// 系统级限流错误码（F4：过载从"静默丢弃"改为显式响应，客户端据此退避重试）。
// 框架系统级错误码使用负数区间，避免与业务正数 code 冲突。
// 载体：WS 走 WSResponse.Code；TCP/KCP 走 v2 响应帧的 code 字段
// （legacy v1 无 code 字段，仅能收到空响应帧）。
const CodeGateBusy int32 = -429

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
	// E 档配置读点迁移：改走新配置系统的类型化字段（原 tgf.GetStrConfig 旧读法）。
	// init 顺序：rpc 包 import tgf → tgf 包 init 先执行 tgf.InitConfig →
	// tgfconfig.Current() 此时保证非零值。
	// 这些是启动期一次性项：连接建立时读取的包级默认值，不做热更
	//（连接级参数热更需逐连接生效语义，归 F 档评估）。
	cfg := tgfconfig.Current()
	if v := cfg.RPC.TCPDeadLineSec; v > 0 {
		defaultDeadLineTime = time.Duration(v) * time.Second
	}
	if v := cfg.RPC.TCPWriteTimeoutMs; v > 0 {
		defaultWriteDeadline = time.Duration(v) * time.Millisecond
	}
	if v := cfg.RPC.TCPSendChanTimeoutMs; v > 0 {
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
	//   - nil：连接存在且已入队；F3 起也包括"用户处于断线重连窗口、推送已缓冲
	//     待重连补发"（延迟交付语义，见 detachedSession）
	//   - tgf.ErrUserNotFound：目标用户在本节点不在线（且不在重连窗口内）
	//   - tgf.ErrConnClosed / tgf.ErrConnSendTimeout：Send 层错误透传
	ToUser(userId, messageType string, data []byte) error
	// DoLogin 把 templateUserId 对应的连接绑定为 userId 并迁移到 Online。
	// F3: 入参增加 resumeToken（客户端重连续联令牌，可为空），返回值增加本次
	// 会话新签发的 resume token——gate.Login 经 LoginRes 返还给业务转交客户端。
	DoLogin(userId, templateUserId, resumeToken string) (newResumeToken string, err error)

	Offline(userId string, replace bool) (exists bool)

	// CloseListeners 停止接收新连接并关闭全部 listener（TCP/WS/KCP）。幂等。
	// D 档（v3）新增的优雅停机钩子——停机编排方（Wave2 / Server.Destroy）先调它
	// 停止 accept，再做 in-flight drain 与在线连接清理。已建立的 TCP/WS 连接
	// **不会**被本方法断开；KCP 例外（见 TCPServer.CloseListeners 的注释）。
	CloseListeners() error
}

type ITCPBuilder interface {
	WithPort(port string) ITCPBuilder
	WithBuffer(readBuffer, writeBuffer int) ITCPBuilder
	WithWSPath(path string) ITCPBuilder
	Address() string
	Port() string
	WsPath() string
	// MaxConnections 是网关（TCP/WS/KCP 三入口合计）的最大并发连接数。
	// F4 起真实生效：handleConn 入口统一计数，超限连接被立即关闭并计入
	// tgf_gate_connections_rejected_total。<=0 表示不限制。
	MaxConnections() int32
	// WithMaxConnections 覆盖最大连接数（F4 新增）。传 <=0 表示不限制。
	WithMaxConnections(max int32) ITCPBuilder
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

	users *hashmap.Map[string, IUserConnectData]
	//
	startup *sync.Once //是否已经启动

	// ---- D 档（v3）优雅停机钩子相关 ----
	// listenerMu 保护下面三个 listener 句柄的读写（Run/startKCPListener 写，
	// CloseListeners 读）。
	listenerMu  sync.Mutex
	tcpListener *net.TCPListener // TCP listener；未启动或 WS 模式为 nil
	wsServer    *http.Server     // WS/WSS 的 http server；非 WS 模式为 nil
	kcpListener io.Closer        // KCP listener（*kcp.Listener）；未启用 KCP 为 nil

	// acceptClosed 置位后表示 CloseListeners 已执行，accept 循环据此区分
	// "正常停机" 与 "异常 accept 错误"。
	acceptClosed atomic.Bool
	// stopAccept 在 CloseListeners 时关闭：唤醒 selectorChan 与 accept 循环的
	// select 分支，保证停机后没有残留的 accept goroutine。
	stopAccept chan struct{}

	// detached 是断线重连窗口内保留的会话软状态表（F3）：key=userId。
	// 自然断线（非替换踢人）的已登录会话进入该表，窗口内 ToUser 推送被缓冲，
	// 凭 resume token 重新登录时按序补发。nil 表示未启用（手工构造的实例）。
	detached *hashmap.Map[string, *detachedSession]

	// connCount 是当前活跃连接数（TCP/WS/KCP 三入口合计），F4 用于 enforce
	// MaxConnections。与 gateConnGauge 的差别：gauge 是观测口径（可能换 Provider），
	// 这里是控制口径——必须是进程内权威原子计数。
	connCount atomic.Int32
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

// WithMaxConnections 覆盖最大并发连接数（F4）。<=0 表示不限制。
func (s *ServerConfig) WithMaxConnections(max int32) ITCPBuilder {
	s.maxConnections = max
	return s
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

	// replaceFlag 仅作为"本次下线是否是被替换登录踢掉"的语义标记，由 Offline 入参
	// 写入，供 OfflineHook 传播与 F3 的断线重连判定（被踢会话不进入重连窗口）用。
	// 只会在 CAS 进入 Offlining 后被赋值一次；F3 改为 atomic.Bool——handleConn 的
	// defer 清理路径会跨 goroutine 读它，普通 bool 在"客户端掉线与踢人并发"时是数据竞争。
	replaceFlag atomic.Bool

	// resumeToken 是本次会话的断线重连令牌（F3），DoLogin 成功路径签发。
	// 在 Idle→LoggingIn 窗口内由登录 goroutine 写入、发布到 users 表后只读。
	resumeToken string

	// macKey 是本会话的帧级 MAC 密钥（F5 帧级防伪），DoLogin 在 markOnline 之前
	// 由 resume token 派生写入（仅当 MAC 开启且传输需要，见 frame_mac.go）。
	// 内存可见性：写发生在 markOnline 的 atomic CAS（release）之前，reader goroutine
	// 仅在观测到 state==Online（acquire）后读取——无数据竞争。nil 表示本会话不启用 MAC。
	macKey []byte
	// macLastSeq 是已验证帧的最大序号（防重放：seq 必须严格递增）。
	// 只被 reader goroutine 读写，无需原子。
	macLastSeq uint64
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
		case <-t.stopAccept:
			// D 档（v3）：CloseListeners 后 selector 退出，不再消费新连接。
			log.InfoTag("tcp", "tcp selector 退出(网关已停止接收新连接)")
			return
		}
	}
}

// CloseListeners 停止接收新连接：关闭 TCP listener、WS http server、KCP listener，
// 并通知 selector / accept goroutine 退出。幂等——重复调用直接返回 nil。
//
// D 档（v3）优雅停机钩子。语义边界：
//   - 已建立的 TCP/WS 连接**不受影响**（http.Server.Close 不触碰被 hijack 的
//     WebSocket 连接；net.TCPListener.Close 只停 accept）——在线会话的清理由
//     停机编排方随后通过 Offline 链路处理；
//   - KCP 是例外：kcp-go 的服务端会话与 listener 共享同一个 UDP socket，
//     关闭 listener 会同时中断既有 KCP 会话。优雅停机场景下网关连接本就要
//     随之断开，可接受；如需先 drain 再断，编排方应在调用本方法前完成。
func (t *TCPServer) CloseListeners() error {
	if !t.acceptClosed.CompareAndSwap(false, true) {
		return nil
	}
	close(t.stopAccept)

	t.listenerMu.Lock()
	tcpL, wsS, kcpL := t.tcpListener, t.wsServer, t.kcpListener
	t.listenerMu.Unlock()

	var firstErr error
	if tcpL != nil {
		if e := tcpL.Close(); e != nil && !errors.Is(e, net.ErrClosed) {
			firstErr = e
		}
	}
	if wsS != nil {
		// Close 立刻关闭 listener；已升级（hijack）的 WS 连接不受影响。
		if e := wsS.Close(); e != nil && !errors.Is(e, http.ErrServerClosed) && firstErr == nil {
			firstErr = e
		}
	}
	if kcpL != nil {
		if e := kcpL.Close(); e != nil && firstErr == nil {
			firstErr = e
		}
	}
	log.InfoTag("tcp", "网关已停止接收新连接(listener 已全部关闭) err=%v", firstErr)
	return firstErr
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
	// F4: MaxConnections 真实生效（审计 P1"死配置"）——TCP/WS/KCP 三入口都汇聚到
	// handleConn，在任何资源（users 表条目、goroutine、gauge）分配之前统一计数；
	// 超限连接立即关闭并计入 tgf_gate_connections_rejected_total。
	if !t.acquireConnSlot() {
		incGateConnRejected()
		log.WarnTag("tcp", "连接数已达上限 max=%d,拒绝新连接 addr=%v (tgf_gate_connections_rejected_total+1)",
			t.config.MaxConnections(), conn.RemoteAddr())
		_ = conn.Close()
		return
	}
	defer t.releaseConnSlot()

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
	// E6：IConn.IsWebSocket 已随编码下沉移除，这里改用 wsTransport 可选接口探测。
	if isWSConn(conn) {
		reqMetaData[tgf.GatewayServiceModuleName] = internal.LocalServerAddress
	}
	connectData.contextData.SetValue(share.ReqMetaDataKey, reqMetaData)
	t.users.Set(templateUserId, connectData)
	log.DebugTag("tcp", "接收到一条新的连接 addr=%v templateUserId=%v ws=%v",
		conn.RemoteAddr(), templateUserId, isWSConn(conn))

	// B4 埋点：活跃连接数 gauge +1；defer -1 保证任何退出路径都配对。
	gauge := getGateConnGauge()
	gauge.Inc()

	// 统一的 reqChan 缓冲策略：原来 WS=10 / TCP=0 两套，phase2 统一为 16（平衡吞吐与背压）。
	reqChan := make(chan *RequestData, 16)

	defer func() {
		if err := recover(); err != nil {
			log.DebugTag("tcp", "连接异常关闭 %v", err)
		}
		// 避免并发情况下，新登录用户数据比旧连接清理先执行。
		// removedSelf==true 意味着本连接确实是 userId 当前注册在线的会话
		//（成功登录过且未被新会话顶替）——这是 F3 会话级收尾的前提条件。
		removedSelf := false
		if tmpUser, ok := t.users.Get(connectData.userId); ok {
			if GetTemplateUserId(tmpUser.GetContextData()) == GetTemplateUserId(connectData.GetContextData()) {
				t.users.Del(connectData.userId)
				removedSelf = true
			}
		}
		connectData.Offline(false)
		// F3: 自然断线（非替换踢人）的会话级收尾——owner meta 原子比对清理（审计8）
		// + 进入断线重连窗口（detached session，缓冲后续推送待补发）。
		// 被踢会话（replaceFlag=true）跳过：owner 由新登录方负责覆盖写，且被替换的
		// 会话没有"重连续联"语义。
		if removedSelf && !connectData.replaceFlag.Load() {
			t.afterNaturalDisconnect(connectData)
		}
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
			// F5: 心跳响应按协议版本编码（v2 带引导头，消除与 compress=1 的字节流歧义）。
			_ = connectData.Send(heartbeatResponseFrame())

		case Logic:
			// F5 帧级防伪：已登录且启用 MAC 的会话必须携带合法 MAC（伪造/重放/缺失
			// 一律视为攻击面，断开连接）。校验在分发给 logic goroutine 之前完成。
			if !checkInboundFrameAuth(connectData, frame) {
				incGateMACReject()
				log.WarnTag("tcp", "用户[%s] 帧级MAC校验失败(伪造/重放/缺失),断开连接 module=%v method=%v addr=%v (tgf_gate_mac_reject_total+1)",
					connectData.userId, frame.Module, frame.Method, conn.RemoteAddr())
				return
			}
			pack := &RequestData{
				RequestMethod: frame.Method,
				Module:        frame.Module,
				Data:          frame.Data,
				User:          connectData,
				ReqId:         frame.ReqId,
			}
			// E3-rpc 埋点：网关入站业务请求量。
			incGateRequest()
			log.DebugTag("tcp", "收到请求[%s.%s]", pack.Module, pack.RequestMethod)
			select {
			case reqChan <- pack:
			case <-connectData.stop:
				return
			default:
				// E3-rpc：背压丢弃原先只打 Debug（生产不可观测）——升 Warn 并计数。
				// F4：丢弃不再静默——向客户端回写 CodeGateBusy 限流响应（尽力而为，
				// 绝不反向阻塞 reader），客户端按 ReqId/messageType 即可感知并退避。
				incGateDropped()
				log.WarnTag("tcp", "用户[%s] 请求处理繁忙,丢弃请求并回写限流码[%s.%s] (tgf_gate_dropped_requests_total+1)",
					connectData.userId, pack.Module, pack.RequestMethod)
				t.replyGateBusy(connectData, pack.Module+"."+pack.RequestMethod, pack.ReqId)
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
			//
			// F3（审计：踢人时序错配）：原实现此处固定 time.Sleep(1s)——比踢人发起方
			// 的等待窗口（200ms）还长，导致旧会话 OfflineHook 晚于新会话上线到达业务。
			// 现改为"有界等待写出队列排空"：通知帧被 writer 取走即继续清理，典型耗时
			// 毫秒级，上界 replaceNotifyDrainWait；本方法因此可以安全地被同步踢人
			// RPC（kickOwnerSyncByAddress → gate.Offline）在 ack 语义下调用。
			if sendErr := oldUser.Send(replaceLoginNotifyFrame()); sendErr == nil {
				if ucd, ok := oldUser.(*UserConnectData); ok {
					ucd.waitSendDrained(replaceNotifyDrainWait)
				}
			}
		}
		//断开已经在线的玩家上下文
		oldUser.Offline(replace)
		exists = true
		log.InfoTag("login", "重复登录,踢掉在线玩家 userId=%v", userId)
	}
	return
}

// acquireConnSlot 尝试占用一个连接配额（F4 MaxConnections）。
// 返回 false 表示已超上限，调用方应立即拒绝该连接。max<=0 表示不限制（仅计数）。
func (t *TCPServer) acquireConnSlot() bool {
	var max int32
	if t.config != nil {
		max = t.config.MaxConnections()
	}
	n := t.connCount.Add(1)
	if max > 0 && n > max {
		t.connCount.Add(-1)
		return false
	}
	return true
}

// releaseConnSlot 归还连接配额，与 acquireConnSlot 成对（handleConn defer 调用）。
func (t *TCPServer) releaseConnSlot() {
	t.connCount.Add(-1)
}

// replyGateBusy 向客户端回写一帧 CodeGateBusy 限流响应（F4 背压显式响应）。
// 用非阻塞入队（trySendNoWait）：过载场景下 writeChan 也可能满，限流通知是
// 尽力而为——绝不能反过来阻塞 reader/logic goroutine 形成二次拥塞。
// 载体限制：WS 走 WSResponse.Code，TCP/KCP 走 v2 响应帧 code 字段；
// legacy v1 协议无 code 字段，客户端只能看到一帧空响应（见 CodeGateBusy 注释）。
func (t *TCPServer) replyGateBusy(u *UserConnectData, messageType string, reqId int32) {
	frame := t.encodeResponseFor(u.conn, messageType, reqId, CodeGateBusy, nil)
	if !u.trySendNoWait(frame) {
		log.DebugTag("tcp", "用户[%s] 限流响应入队失败(writeChan 亦满或连接已关闭) msgType=%v", u.userId, messageType)
	}
}

func (t *TCPServer) DoLogin(userId, templateUserId, resumeToken string) (string, error) {
	userData, _ := t.users.Get(templateUserId)
	if userData == nil {
		return "", errors.New("用户不存在")
	}
	// A3-phase1: 登录入口必须能 CAS 到 LoggingIn——意味着连接处于 Idle 状态，
	// 没有被其他 Login / Offline 抢占。否则拒绝登录。
	u, ok := userData.(*UserConnectData)
	if !ok {
		return "", errors.New("用户连接类型异常")
	}
	if !u.tryBeginLogin() {
		return "", fmt.Errorf("连接状态异常,无法登录 state=%d", u.loadState())
	}

	// F3: 签发本次会话的 resume token，并在把连接发布到 users 表之前消费断线
	// 窗口内的 detached session——缓冲补发先于任何新推送入队，保持下行顺序。
	//（此时 writer goroutine 已随 handleConn 启动，Send 可用。）
	newResumeToken := util.GenerateSnowflakeId()
	u.resumeToken = newResumeToken
	// F5 帧级防伪：按会话派生 MAC 密钥（默认随登录鉴权开启；WS 与 KCP+AEAD 等
	// 自带帧鉴真/无 MAC 帧格式的传输豁免，见 frame_mac.go）。写入必须在 markOnline
	// 的 CAS 之前——reader goroutine 观测到 Online 后才会读 macKey（acquire/release）。
	if frameMACActive() && connRequiresFrameMAC(u.conn) {
		u.macKey = DeriveFrameMACKey(newResumeToken)
	}
	t.consumeDetachedSession(u, userId, resumeToken)

	ct := u.GetContextData()
	ct.SetReqMetaData(tgf.ContextKeyUserId, userId)
	// F4: LogicSync __hash 注入（审计8 P1）——rpcx fork 的 LogicSync 按
	// share.ContextKeyHash("__hash") 散列选串行锁槽，但网关流量原先从不注入该
	// meta，宣称的"按用户串行"退化为按连接散列。这里按 userId 注入后，该连接
	// 后续所有经 sendMessage 发出的 RPC（ReqMetaData 随 share.Context 透传）都
	// 携带 __hash=userId，与 NewUserRPCContext 的服务侧语义对齐：同一用户串行、
	// 不同用户并行（服务端语义测试见 rpcx/server/logicsync_test.go）。
	ct.SetReqMetaData(tgf.ContextKeyHash, userId)
	t.users.Set(userId, u)
	u.Login(userId)

	// A3-phase1: LoggingIn → Online 的 CAS 收尾。
	// 失败通常意味着 Login 调用过程中另一个路径（例如 ToUser panic、Send 超时）
	// 已经把连接标记为 Offlining——这种情况下我们回滚 users 表条目并返回错误。
	if !u.markOnline() {
		t.users.Del(userId)
		return "", fmt.Errorf("登录过程中连接被中止 state=%d", u.loadState())
	}

	// 成功后清理 templateUserId 的索引条目
	t.users.Del(templateUserId)
	log.InfoTag("tcp", "login templateUserId %v , uuid %v", templateUserId, userId)
	return newResumeToken, nil
}

func (t *TCPServer) doLogic(data *RequestData) {
	// A3-phase1: 只对处于 Idle/Online 状态的连接处理请求。Offlining 状态可能
	// 是 logic goroutine 还没退出但 Offline 已经触发，拒绝请求避免对已死连接
	// 做无意义的下游调用。LoggingIn 状态拒绝是为了避免登录过程中客户端抢发业务请求。
	if !data.User.isActiveForLogic() {
		// E3-rpc 埋点：inactive 连接上的丢弃与背压丢弃共用同一计数器。
		incGateDropped()
		log.DebugTag("tcp", "drop request on inactive connection user=%v state=%d module=%v method=%v",
			data.User.userId, data.User.loadState(), data.Module, data.RequestMethod)
		// F4: 显式响应——LoggingIn 等"暂不可处理"状态回写限流码让客户端退避重试；
		// Offlining 是终态（连接即将关闭），回写无意义，跳过。
		if data.User.loadState() != sessionOfflining {
			t.replyGateBusy(data.User, data.Module+"."+data.RequestMethod, data.ReqId)
		}
		return
	}

	var (
		err         error
		messageType = data.Module + "." + data.RequestMethod
	)
	data.StartTime = time.Now()
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
	reply := resData.ByteData
	// E6：响应编码下沉到连接适配器（conn.EncodeResponse），不再经由 server 级
	// IsWebSocket 分支——同一 GateService 下 TCP/KCP/WS 连接各取所需的帧格式。
	clientData := t.encodeResponseFor(data.User.conn, messageType, data.ReqId, resData.Code, reply)
	// phase3: Send 返回 error 时记录日志——连接此时要么正在 Offline，
	// 要么 writer 已卡死由 Send 内部触发 Offline，无需额外处理。
	if sendErr := data.User.Send(clientData); sendErr != nil {
		log.DebugTag("tcp", "doLogic 响应推送失败 user=%v module=%v method=%v err=%v",
			data.User.userId, data.Module, data.RequestMethod, sendErr)
	}
}

// wsTransport 是 WebSocket 传输的可选标记接口（E6）。
// 编码差异已下沉为 IConn.EncodeResponse，IConn 不再声明 IsWebSocket；
// 仅剩的"WS 连接接入时往 meta 写本地 gate 地址"历史行为差异通过本接口探测。
type wsTransport interface{ IsWebSocket() bool }

// isWSConn 判断一个连接是否 WebSocket 传输。
func isWSConn(conn IConn) bool {
	w, ok := conn.(wsTransport)
	return ok && w.IsWebSocket()
}

// encodeWSResponseFrame 编码 WS 下行响应（WSResponse proto）。
//
// D2 / P0-1 约束：proto.Marshal 独立分配，返回值与任何共享存储零别名。
// 压缩失败降级发送未压缩原文（原实现吞错仍标 Zip=true 发坏数据）；
// 压缩阈值与二进制分支统一为 `>= compressMinSize`。
func encodeWSResponseFrame(messageType string, reqId, code int32, reply []byte) []byte {
	data := &WSResponse{}
	data.MessageType = messageType
	if len(reply) >= compressMinSize {
		if zipped, zipErr := util2.Zip(reply); zipErr == nil {
			reply = zipped
			data.Zip = true
		} else {
			log.WarnTag("tcp", "WS响应压缩失败,降级发送未压缩数据 msgType=%v err=%v", messageType, zipErr)
		}
	}

	data.Data = reply
	data.ReqId = reqId
	data.Code = code
	res, _ := proto.Marshal(data)
	return res
}

// encodeBinaryResponseFrame 编码 TCP / KCP 下行二进制响应帧。
//
// F5 协议修缮：默认输出 v2 帧（带 [magic][messageType] 引导头 + int32 code），
// UseLegacyResponseProtocol(true) 时输出 v1 旧帧（无引导头、无 code）——两种格式
// 见 ResponseHeader 类型注释。v2 的引导头消除了"心跳 0x01 vs compress=1"的字节流
// 歧义；code 字段让 F4 的限流响应（CodeGateBusy）对 TCP/KCP 客户端可见。
//
// D2 / P0-1 修复（v3）：原实现用 bytebufferpool 编码后 `res = bp.Bytes()` 直接返回
// 池化 buffer 的内部切片，函数返回（defer Put）即归还池——而调用方 doLogic / ToUser
// 随后才把 res 推入 writeChan，由 writer goroutine 在之后任意时刻异步写出。期间任何
// 其他连接从池里 Get 到同一 buffer 并写入，就会篡改仍在队列里的帧，造成跨连接串包
// （A 玩家收到 B 玩家的数据）。修复方式：彻底移除该处 bytebufferpool，按精确容量
// 一次性分配独立切片返回——返回值与任何共享存储零别名，可被安全地异步消费。
//
// 顺手修复（原 P3）：压缩失败原先 return 空帧（客户端该请求的响应静默消失）——
// 现在降级为发送未压缩原文。
func encodeBinaryResponseFrame(messageType string, code int32, reply []byte) []byte {
	var compress byte = 0
	if len(reply) >= compressMinSize {
		if zipped, zipErr := util2.Zip(reply); zipErr == nil {
			reply = zipped
			compress = 1
		} else {
			log.WarnTag("tcp", "TCP响应压缩失败,降级发送未压缩数据 msgType=%v err=%v", messageType, zipErr)
		}
	}

	mtSize := len(messageType)
	v2 := responseProtocolIsV2()
	// v2: 2B 引导头 + 1B compress + 4B code + 2B method size + 4B data size + method + data
	// v1: 1B compress + 2B method size + 4B data size + method + data
	capacity := 1 + 2 + 4 + mtSize + len(reply)
	if v2 {
		capacity += 2 + 4
	}
	res := make([]byte, 0, capacity)
	if v2 {
		// 引导头：协议版本/类型位（响应 magic + messageType）
		res = append(res, responseMagicNumber, byte(Logic))
	}
	//是否压缩
	res = append(res, compress)
	if v2 {
		//业务 code（v2 新增，限流/错误码载体）
		var codeBytes [4]byte
		binary.BigEndian.PutUint32(codeBytes[:], uint32(code))
		res = append(res, codeBytes[:]...)
	}
	//响应函数长度
	var rqBytes [2]byte
	binary.BigEndian.PutUint16(rqBytes[:], uint16(mtSize))
	res = append(res, rqBytes[:]...)
	//响应内容长度
	var dataBytes [4]byte
	binary.BigEndian.PutUint32(dataBytes[:], uint32(len(reply)))
	res = append(res, dataBytes[:]...)
	//响应函数名
	res = append(res, messageType...)
	//响应内容
	res = append(res, reply...)
	return res
}

// getSendToClientData 把一次逻辑响应编码成客户端帧——按 server 配置推断格式。
//
// E6 之后这是**回退路径**：正常流量走 conn.EncodeResponse（编码已下沉到连接
// 适配器，见 encodeResponseFor）；本方法保留给"拿不到 IConn"的场景（单测 mock、
// conn 为 nil 的连接数据）。
func (t *TCPServer) getSendToClientData(messageType string, reqId, code int32, reply []byte) (res []byte) {
	if t.config.IsWebSocket() {
		return encodeWSResponseFrame(messageType, reqId, code, reply)
	}
	return encodeBinaryResponseFrame(messageType, code, reply)
}

// encodeResponseFor 优先用连接自身的帧编码（E6：每个 IConn 实现自带格式），
// conn 为 nil（单测 mock / 无连接场景）时回退到按 server 配置推断。
func (t *TCPServer) encodeResponseFor(conn IConn, messageType string, reqId, code int32, reply []byte) []byte {
	if conn != nil {
		return conn.EncodeResponse(messageType, reqId, code, reply)
	}
	return t.getSendToClientData(messageType, reqId, code, reply)
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
			// D 档（v3）：WS 改用专属 mux + 可关闭的 http.Server，不再向全局
			// DefaultServeMux 注册（避免与 pprof / 其他组件的全局路由互相污染），
			// 并把 server 句柄存下来供 CloseListeners 优雅停机使用。
			mux := http.NewServeMux()
			mux.HandleFunc("/"+t.config.WsPath(), t.wsHandler)
			srv := &http.Server{
				Addr:    t.config.Address() + ":" + t.config.Port(),
				Handler: mux,
			}
			t.listenerMu.Lock()
			t.wsServer = srv
			t.listenerMu.Unlock()

			util.Go(func() {
				log.InfoTag("init", "启动ws服务 %v", t.config.Address()+":"+t.config.Port()+"/"+t.config.WsPath())
				// 启动服务器
				var err error
				if t.config.IsWss() {
					err = srv.ListenAndServeTLS(t.config.WssCertFile(), t.config.WssKeyFile())
				} else {
					err = srv.ListenAndServe()
				}

				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					// 端口被占等启动失败必须是显式错误而不是静默——原实现只打 Info。
					log.Error("[init] ws服务 启动失败 addr=%v err=%v", srv.Addr, err)
					return
				}
			})
		} else {
			add, resolveErr := net.ResolveTCPAddr("tcp", fmt.Sprintf("%v:%v", t.config.Address(), t.config.Port()))
			if resolveErr != nil {
				log.Error("[init] tcp服务 地址解析失败 addr=%v:%v err=%v",
					t.config.Address(), t.config.Port(), resolveErr)
				return
			}
			listen, err := net.ListenTCP("tcp", add)
			if err != nil {
				// 原实现是 DebugTag——端口被占时进程"健康"运行但网关没监听，无从感知。
				log.Error("[init] tcp服务 启动异常 addr=%v err=%v", add, err)
				return
			}
			t.listenerMu.Lock()
			t.tcpListener = listen
			t.listenerMu.Unlock()
			log.InfoTag("init", "tcp服务 启动成功 %v", listen.Addr())

			//启动selector线程，等待连接接入
			util.Go(func() {
				log.InfoTag("init", "tcp selector 启动成功")
				t.selectorChan()
			})

			util.Go(func() {
				log.InfoTag("init", "tcp 开始监听连接")
				// D 档（v3）accept 循环加固：
				//   - 原实现 `tcp, _ := listen.AcceptTCP()` 吞错误——出错时 nil 连接
				//     被推进 conChan 引发 handleConn panic + users 表泄漏，持续性错误
				//     （fd 耗尽等）下变成 CPU 热循环；
				//   - 现在错误分级：listener 关闭 → 正常退出；其他错误 → 指数退避重试；
				//     nil 连接永不入管道。
				var tempDelay time.Duration
				for {
					tcp, acceptErr := listen.AcceptTCP()
					if acceptErr != nil {
						if t.acceptClosed.Load() || errors.Is(acceptErr, net.ErrClosed) {
							log.InfoTag("tcp", "tcp listener 已关闭,accept 循环退出")
							return
						}
						if tempDelay == 0 {
							tempDelay = 5 * time.Millisecond
						} else {
							tempDelay *= 2
						}
						if tempDelay > time.Second {
							tempDelay = time.Second
						}
						log.WarnTag("tcp", "AcceptTCP 错误,%v 后重试 err=%v", tempDelay, acceptErr)
						time.Sleep(tempDelay)
						continue
					}
					tempDelay = 0
					if configErr := t.configureTCPConnection(tcp); configErr != nil {
						log.WarnTag("tcp", "configure accepted connection failed remote=%v err=%v", tcp.RemoteAddr(), configErr)
						_ = tcp.Close()
						continue
					}
					select {
					case t.conChan <- tcp: //将链接放入管道中
					case <-t.stopAccept:
						_ = tcp.Close()
						return
					}
				}
			})
		}

	})
}

func (t *TCPServer) configureTCPConnection(conn *net.TCPConn) error {
	if err := conn.SetNoDelay(true); err != nil {
		return fmt.Errorf("set no-delay: %w", err)
	}
	if err := conn.SetKeepAlive(true); err != nil {
		return fmt.Errorf("set keepalive: %w", err)
	}
	if err := conn.SetReadBuffer(t.config.ReadBufferSize()); err != nil {
		return fmt.Errorf("set read buffer: %w", err)
	}
	if err := conn.SetWriteBuffer(t.config.WriteBufferSize()); err != nil {
		return fmt.Errorf("set write buffer: %w", err)
	}
	if err := conn.SetDeadline(time.Now().Add(t.config.DeadLineTime())); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	return nil
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
		// F3: 用户不在线但处于断线重连窗口内——缓冲推送等待重连补发，
		// 对调用方呈现"已接收（延迟交付）"语义。
		if t.bufferDetachedPush(userId, messageType, data) {
			log.DebugTag("tcp", "userid=%v 处于断线重连窗口,推送已缓冲待补发 msgType=%v", userId, messageType)
			return nil
		}
		log.DebugTag("tcp", "userid=%v user connection not found", userId)
		return tgf.ErrUserNotFound
	}
	// E6：推送编码同样下沉到连接适配器；非 *UserConnectData（测试替身）或
	// conn 为 nil 时回退 server 配置推断。
	var conn IConn
	if ucd, isUCD := connectData.(*UserConnectData); isUCD {
		conn = ucd.conn
	}
	res := t.encodeResponseFor(conn, messageType, 0, 0, data)
	return connectData.Send(res)
}

// ============================================================================
// F3: 断线重连语义（session resume + 重连窗口内消息缓冲）
//
// 设计（对应审计"断线重连语义缺失"与路线图 F3 验收"断线 30s 内重连不丢推送"）：
//   - DoLogin 成功即签发 resume token（经 LoginRes 返还业务转交客户端）。
//   - 已登录会话**自然断线**（非替换踢人）时进入 detached 表，保留 resumeWindow；
//     窗口内对该 userId 的 ToUser 推送进缓冲（上限 resumeBufferLimit，溢出丢最旧）。
//   - 客户端重连后重新走登录流程（凭据照常校验），LoginReq.ResumeToken 与
//     detached 表中令牌匹配 → 缓冲按序补发到新连接；不匹配 / 缺省 / 窗口已过 →
//     按全新会话处理，缓冲丢弃。
//   - 边界：缓冲是**节点本地**的——重连落到其他网关节点时缓冲不迁移（owner meta
//     已被下线清理，新节点正常登录，仅丢失旧节点缓冲）；跨节点会话迁移属后续档。
// ============================================================================

var (
	// resumeWindow 是断线重连窗口。<=0 表示关闭 resume 能力。
	// 默认 30s 对应 F3 验收口径"断线 30s 内重连不丢推送"。
	resumeWindow = 30 * time.Second
	// resumeBufferLimit 是单个 detached session 最多缓冲的推送条数，超出丢最旧
	//（重连后玩家更需要最新状态），丢弃量在补发时告警。
	resumeBufferLimit = 256
)

// bufferedPush 缓冲一条尚未编码的下行推送。存原始 (messageType, data) 而非编码
// 后的帧——重连后的新连接可能是不同传输（TCP/WS/KCP），补发时按新连接编码。
type bufferedPush struct {
	messageType string
	data        []byte
}

// detachedSession 是断线后保留的会话软状态。
type detachedSession struct {
	mu       sync.Mutex
	userId   string
	token    string // 断线会话的 resume token，重连登录时比对
	pushes   []bufferedPush
	dropped  int // 缓冲溢出丢弃的条数（丢最旧）
	expireAt time.Time
	timer    *time.Timer // 窗口到期自动从 detached 表移除
}

// afterNaturalDisconnect 是自然断线（非替换踢人）会话的统一收尾，
// 由 handleConn 的 defer 在 removedSelf && !replace 条件下调用：
//  1. owner meta 原子比对清理（审计8：网关重启不再残留永久脏 owner）；
//  2. 进入断线重连窗口（detached session）。
func (t *TCPServer) afterNaturalDisconnect(u *UserConnectData) {
	if u.userId == "" {
		return
	}
	// 仅当 owner 仍指向本节点时删除（Lua 原子比对），并发新登录写入的新 owner 不受影响。
	loginCoord.ClearGateOwner(u.userId, localGateAddress())
	t.maybeDetachSession(u)
}

// maybeDetachSession 把一个自然断线的已登录会话放入重连窗口。
func (t *TCPServer) maybeDetachSession(u *UserConnectData) {
	if t.detached == nil || resumeWindow <= 0 {
		return
	}
	if u.userId == "" || u.resumeToken == "" {
		return
	}
	ds := &detachedSession{
		userId:   u.userId,
		token:    u.resumeToken,
		expireAt: time.Now().Add(resumeWindow),
	}
	t.detached.Set(u.userId, ds)
	ds.mu.Lock()
	ds.timer = time.AfterFunc(resumeWindow, func() {
		if cur, ok := t.detached.Get(ds.userId); ok && cur == ds {
			t.detached.Del(ds.userId)
		}
	})
	ds.mu.Unlock()
	// 与并发新登录的竞态收窄：若此刻该 userId 的新会话已上线（重连先于本清理
	// 路径完成），立即回收刚建的 detach 条目（缓冲必为空，无需补发）。
	if _, online := t.users.Get(u.userId); online {
		if cur, ok := t.detached.Get(u.userId); ok && cur == ds {
			t.detached.Del(u.userId)
			ds.mu.Lock()
			ds.timer.Stop()
			ds.mu.Unlock()
		}
	}
}

// bufferDetachedPush 在重连窗口内缓冲一条推送。返回 true 表示已缓冲。
// data 做防御性拷贝——调用方的切片所有权不进入缓冲生命周期。
func (t *TCPServer) bufferDetachedPush(userId, messageType string, data []byte) bool {
	if t.detached == nil {
		return false
	}
	ds, ok := t.detached.Get(userId)
	if !ok {
		return false
	}
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if time.Now().After(ds.expireAt) {
		return false
	}
	if len(ds.pushes) >= resumeBufferLimit {
		// 溢出丢最旧；丢弃量留底，补发时告警提示业务缓冲不足。
		over := len(ds.pushes) - resumeBufferLimit + 1
		ds.pushes = append(ds.pushes[:0], ds.pushes[over:]...)
		ds.dropped += over
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	ds.pushes = append(ds.pushes, bufferedPush{messageType: messageType, data: cp})
	return true
}

// consumeDetachedSession 在 DoLogin（持登录锁）内消费 detached 条目：
// resumeToken 匹配且窗口未过 → 把缓冲按序补发到新连接；否则按全新会话丢弃缓冲。
// 无论是否补发，条目都被消费移除（每次登录都签发新 token，旧窗口随之关闭）。
func (t *TCPServer) consumeDetachedSession(u *UserConnectData, userId, resumeToken string) {
	if t.detached == nil {
		return
	}
	ds, ok := t.detached.Get(userId)
	if !ok {
		return
	}
	t.detached.Del(userId)
	ds.mu.Lock()
	if ds.timer != nil {
		ds.timer.Stop()
	}
	pushes := ds.pushes
	dropped := ds.dropped
	token := ds.token
	expireAt := ds.expireAt
	ds.pushes = nil
	ds.mu.Unlock()

	if resumeToken == "" || resumeToken != token || time.Now().After(expireAt) {
		if len(pushes) > 0 {
			log.DebugTag("tcp", "用户 %v 以全新会话登录(令牌缺省/不符/窗口已过),丢弃断线缓冲 %d 条",
				userId, len(pushes))
		}
		return
	}
	if dropped > 0 {
		log.WarnTag("tcp", "用户 %v 断线缓冲曾溢出,最早的 %d 条推送已丢失(resumeBufferLimit=%d)",
			userId, dropped, resumeBufferLimit)
	}
	for _, p := range pushes {
		frame := t.encodeResponseFor(u.conn, p.messageType, 0, 0, p.data)
		if sendErr := u.Send(frame); sendErr != nil {
			log.WarnTag("tcp", "用户 %v 重连补发中断 err=%v", userId, sendErr)
			return
		}
	}
	if len(pushes) > 0 {
		log.InfoTag("tcp", "用户 %v 断线重连补发完成 count=%d", userId, len(pushes))
	}
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
	u.replaceFlag.Store(replace)

	for _, key := range u.contextData.GetAllReqMetaDataKeys() {
		if err := SendRPCMessageByStr(u.contextData, key, "OfflineHook",
			&OfflineReq{UserId: u.userId, Replace: replace}, &EmptyReply{}); err != nil {
			log.WarnTag("tcp", "offline hook failed userId=%v module=%v err=%v", u.userId, key, err)
		}
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
		if err != nil && !errors.Is(err, tgf.ErrServiceNotFound) {
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

// trySendNoWait 非阻塞入队：连接已关闭或 writeChan 已满时立即返回 false。
// F4 背压显式响应专用——过载时的限流通知绝不能反向阻塞 reader/logic goroutine
// （Send 的 3s 入队等待在过载场景会形成二次拥塞甚至误触发 Offline）。
func (u *UserConnectData) trySendNoWait(data []byte) bool {
	select {
	case <-u.stop:
		return false
	default:
	}
	select {
	case u.writeChan <- data:
		return true
	case <-u.stop:
		return false
	default:
		return false
	}
}

func (u *UserConnectData) StartReq() {
	u.contextData.SetReqMetaData(tgf.ContextKeyTRACEID, util.GenerateSnowflakeId())
}

// F3: 替换登录通知（ReplaceLogin 帧）的有界排空参数。
// 原实现固定 time.Sleep(1s)；现在最多等 replaceNotifyDrainWait——
// 通知帧被 writer goroutine 取走（典型毫秒级）即继续清理。var 便于单测调整。
var (
	replaceNotifyDrainWait = 200 * time.Millisecond
	sendDrainPollInterval  = 2 * time.Millisecond
)

// waitSendDrained 等待 writeChan 排空（writer 已取走全部待发帧），最多等 maxWait。
// 排空后再留一个轮询间隔的宽限，给 writer 正在进行中的底层 WriteFrame 一点收尾
// 时间（WriteFrame 自带 write deadline，不会无限阻塞）。
func (u *UserConnectData) waitSendDrained(maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for len(u.writeChan) > 0 && time.Now().Before(deadline) {
		time.Sleep(sendDrainPollInterval)
	}
	time.Sleep(sendDrainPollInterval)
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
	server.startup = new(sync.Once)
	server.users = hashmap.New[string, IUserConnectData]()
	server.stopAccept = make(chan struct{})
	// F3: 断线重连窗口的会话软状态表
	server.detached = hashmap.New[string, *detachedSession]()
	return server
}
