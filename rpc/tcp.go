package rpc

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/share"
	util2 "github.com/smallnest/rpcx/util"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"github.com/valyala/bytebufferpool"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/runtime/protoiface"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
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

var upGrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024 * 8,
}

type Args[T protoiface.MessageV1] struct {
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

type Reply[T protoiface.MessageV1] struct {
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
	compressMinSize = 1024 * 4

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
	defaultDeadLineTime   = time.Second * 300
	defaultMaxConnections = 10000
)

type IUserConnectData interface {
	UpdateUserNodeId(servicePath, nodeId string)
	GetContextData() *share.Context
	GetChannel() chan *client.Call
	Offline(userHook IUserHook)
	Send(data []byte)
	IsLogin() bool
	Login(userId string)
	Stop()
}

type ITCPService interface {
	Run()
	UpdateUserNodeInfo(userId, servicePath, nodeId string) bool
	ToUser(userId, messageType string, data []byte)
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
	DeadLineTime() time.Duration
	ReadBufferSize() int
	WriteBufferSize() int
	IsWebSocket() bool
	SetUserHook(userHook IUserHook)
	UserHook() IUserHook
}

type TCPServer struct {
	config  ITCPBuilder       //tcp连接配置
	conChan chan *net.TCPConn //客户端连接chan

	closeChan chan bool //关闭chan
	users     *hashmap.Map[string, IUserConnectData]

	userHook IUserHook
	//
	startup *sync.Once //是否已经启动
}

type ServerConfig struct {
	address         string //地址
	port            string //端口
	wsPath          string //wsPath
	maxConnections  int32  //最大连接数
	deadLineTime    time.Duration
	readBufferSize  int
	writeBufferSize int
	netType         int
	//

	userHook IUserHook
}

func (s *ServerConfig) SetUserHook(userHook IUserHook) {
	s.userHook = userHook
}
func (s *ServerConfig) UserHook() IUserHook {
	return s.userHook
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

type UserConnectData struct {
	conn        *net.TCPConn
	wsConn      *websocket.Conn
	reqCount    int32
	reader      *bufio.Reader
	userId      string
	contextData *share.Context
	reqChan     chan *client.Call
	stop        chan struct{}
}

type RequestData struct {
	User          *UserConnectData
	RequestMethod string
	Module        string
	Data          []byte
	MessageType   HeaderMessageType
	ReqId         int32
}

func (t *TCPServer) selectorChan() {
	for {
		select {
		case con := <-t.conChan:
			util.Go(func() {
				t.handlerConn(con)
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

func (t *TCPServer) handlerWSConn(conn *websocket.Conn) {
	var ()
	connectData := &UserConnectData{
		wsConn:      conn,
		reqCount:    0,
		contextData: share.NewContext(context.Background()),
		reqChan:     make(chan *client.Call, 1), // 限制用户的请求处于并行状态
		stop:        make(chan struct{}),
	}
	reqMetaData := make(map[string]string)
	reqChan := make(chan *RequestData)
	reqMetaData[tgf.ContextKeyTemplateUserId] = util.GenerateSnowflakeId()
	connectData.contextData.SetValue(share.ReqMetaDataKey, reqMetaData)
	t.users.Set(reqMetaData[tgf.ContextKeyTemplateUserId], connectData)
	log.DebugTag("tcp", "接收到一条新的连接 addr=%v , templateUserId=%v", conn.RemoteAddr().String(), reqMetaData[tgf.ContextKeyTemplateUserId])
	defer func() {
		if err := recover(); err != nil {
			log.DebugTag("tcp", "tcp连接异常关闭 %v", err)
		}
		//避免并发情况下,新登录用户数据比移除用户先执行
		if tmpUser, ok := t.users.Get(connectData.userId); ok {
			if GetTemplateUserId(tmpUser.GetContextData()) == GetTemplateUserId(connectData.GetContextData()) {
				t.users.Del(connectData.userId)
			}
		}
		//
		connectData.Offline(t.userHook)
	}()

	conn.SetPingHandler(func(message string) error {

		err := conn.WriteControl(websocket.PongMessage, []byte(message), time.Now().Add(t.config.DeadLineTime()))
		log.DebugTag("tcp", "收到客户端的ping请求 %v err=%v", message, err)
		if err == websocket.ErrCloseSent {
			return nil
		} else if e, ok := err.(net.Error); ok && e.Timeout() {
			return nil
		}
		return err
	})
	////设置pong,响应客户端的ping心跳
	//conn.SetPongHandler(func(m string) error {
	//	log.DebugTag("tcp", "收到客户端的ping请求 %v", m)
	//	conn.SetReadDeadline(time.Now().Add(t.config.DeadLineTime()))
	//	conn.SetWriteDeadline(time.Now().Add(t.config.DeadLineTime()))
	//	return nil
	//})

	//收到关闭消息后的处理
	conn.SetCloseHandler(func(code int, text string) error {
		log.DebugTag("tcp", "收到客户端的主动关闭连接消息 code=%v text=%v", code, text)
		return nil
	})

	//逻辑处理
	go func() {
		for {
			select {
			case req := <-reqChan:
				t.doLogic(req)
			case <-connectData.stop:
				close(connectData.stop)
				close(reqChan)
				return
			}
		}
	}()

	for {
		// 读取客户端发送的消息
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Info("%v", err)
			break
		}
		switch messageType {
		case websocket.BinaryMessage:
			data := &WSMessage{}
			err := proto.Unmarshal(message, data)
			if err != nil {
				return
			}
			////请求协议格式
			pack := &RequestData{
				RequestMethod: data.ServiceName,
				Module:        data.Module,
				Data:          data.Data,
				User:          connectData,
				ReqId:         data.ReqId,
			}
			log.DebugTag("tcp", "Logic 完整包数据 [%v]", pack)
			reqChan <- pack
		case websocket.PingMessage:
			log.InfoTag("tcp", "收到ping请求 %v", message)
		case websocket.CloseMessage:
			log.InfoTag("tcp", "收到结束连接请求 %v", message)
		default:
			log.DebugTag("tcp", "收到不支持的消息:msType %v   ----   %s", messageType, message)
		}

		//// 将消息原样返回给客户端
		//err = conn.WriteMessage(messageType, message)
		//if err != nil {
		//	log.Info("%v", err)
		//	break
		//}
	}

}

func (t *TCPServer) handlerConn(conn *net.TCPConn) {
	var (
		err                  error
		head                 []byte
		methodSize, dataSize uint16
		rdLen                int
	)

	connectData := &UserConnectData{
		conn:        conn,
		reqCount:    0,
		reader:      bufio.NewReader(conn),
		contextData: share.NewContext(context.Background()),
		reqChan:     make(chan *client.Call, 1), // 限制用户的请求处于并行状态
		stop:        make(chan struct{}),
	}
	reqMetaData := make(map[string]string)
	reqMetaData[tgf.ContextKeyTemplateUserId] = util.GenerateSnowflakeId()
	connectData.contextData.SetValue(share.ReqMetaDataKey, reqMetaData)
	t.users.Set(reqMetaData[tgf.ContextKeyTemplateUserId], connectData)
	log.DebugTag("tcp", "接收到一条新的连接 addr=%v , templateUserId=%v", conn.RemoteAddr().String(), reqMetaData[tgf.ContextKeyTemplateUserId])
	//

	reqChan := make(chan *RequestData)
	defer func() {
		if err := recover(); err != nil {
			log.DebugTag("tcp", "tcp连接异常关闭 %v", err)
		}
		//避免并发情况下,新登录用户数据比移除用户先执行
		if tmpUser, ok := t.users.Get(connectData.userId); ok {
			if GetTemplateUserId(tmpUser.GetContextData()) == GetTemplateUserId(connectData.GetContextData()) {
				t.users.Del(connectData.userId)
			}
		}
		connectData.Offline(t.userHook)
	}()

	go func() {
		for {
			select {
			case req := <-reqChan:
				t.doLogic(req)
			case <-connectData.stop:
				close(connectData.stop)
				close(reqChan)
				return
			}
		}
	}()
	failSignal := false
	for {
		head, err = connectData.reader.Peek(2)
		if err != nil && err != io.EOF {
			log.WarnTag("tcp", "请求头读取数据异常,强制断开连接 %v", err)
			break
		}

		//请求魔法值
		magicNumber := head[0]
		if magicNumber != requestMagicNumber {
			log.InfoTag("tcp", "请求头magic number错误,强制断开连接 %v", err)
			break
		}
		//请求消息类型
		messageType := head[1]
		switch messageType {
		case byte(Heartbeat): //处理心跳逻辑
			connectData.reader.Discard(2)
			connectData.conn.SetDeadline(time.Now().Add(t.config.DeadLineTime()))
			connectData.conn.Write(heartbeatData)
			continue
		case byte(Logic): //处理请求业务逻辑
			head, err = connectData.reader.Peek(int(requestHeadSize))
			if err != nil {
				log.DebugTag("tcp", "Logic 请求头数据异常,强制断开连接 %v", err)
				return
			}
			//	[1][1][2][2][n][n]
			//magic number|message type|request method name size|data size|method name|data
			methodSize = binary.BigEndian.Uint16(head[2:4])
			dataSize = binary.BigEndian.Uint16(head[4:6])
			//算出完整包长度
			totalLen := requestHeadSize + methodSize + dataSize
			if connectData.reader.Buffered() < int(totalLen) {
				if !failSignal {
					log.DebugTag("tcp", "Logic 包长度不足，重新读取等待长度足够 %v--%v", connectData.reader.Buffered(), totalLen)
					connectData.conn.SetDeadline(time.Now().Add(time.Second * 3))
					failSignal = true
				} else {
					_, err = connectData.reader.Peek(int(totalLen))
					if err != nil {
						log.DebugTag("tcp", "Logic 请求头数据异常,强制断开连接 %v", err)
						return
					}
				}
				continue
			}
			allData := make(RequestHeader, totalLen)
			rdLen, err = connectData.reader.Read(allData)
			if rdLen != int(totalLen) || err != nil {
				if !failSignal {
					log.DebugTag("tcp", "Logic 包长度不足 有异常 %v--%v", connectData.reader.Buffered(), totalLen)
					connectData.conn.SetDeadline(time.Now().Add(time.Second * 5))
					failSignal = true
				}
				continue
			}
			reqNameIndex := methodSize + 6
			reqName := util.ConvertStringByByteSlice(allData[6:reqNameIndex])
			ix := strings.LastIndex(reqName, ".")
			reqMethodName := reqName[ix+1:]
			reqModule := reqName[:ix]
			pack := &RequestData{
				RequestMethod: reqMethodName,
				Module:        reqModule,
				Data:          allData[reqNameIndex:],
				User:          connectData,
			}
			log.DebugTag("tcp", "Logic 完整包数据 [%v]", pack)
			if failSignal {
				connectData.conn.SetDeadline(time.Now().Add(time.Second))
			}
			failSignal = false
			reqChan <- pack
		default:
			er := errors.New(fmt.Sprintf("message type error %v", messageType))
			panic(er)
		}
		connectData.reqCount++
	}
}

func (t *TCPServer) SetUserHook(userHook IUserHook) {
	t.userHook = userHook
}

func (t *TCPServer) Offline(userId string, replace bool) (exists bool) {
	oldUser, _ := t.users.Get(userId)
	if oldUser != nil {
		var userHook IUserHook
		if !replace {
			userHook = t.userHook
		} else {
			//发送重复登录消息通知
			oldUser.Send(replaceLoginData)
		}
		//断开已经在线的玩家上下文
		oldUser.Offline(userHook)
		exists = true
		log.InfoTag("login", "重复登录,踢掉在线玩家 userId=%v", userId)
	}
	return
}
func (t *TCPServer) DoLogin(userId, templateUserId string) (err error) {
	var (
		reqMetaDataKey string
	)
	userData, _ := t.users.Get(templateUserId)
	if userData == nil {
		return errors.New("用户不存在")
	}

	ct := userData.GetContextData()
	//
	reqMetaDataKey = fmt.Sprintf(tgf.RedisKeyUserNodeMeta, userId)
	reqMetaData, suc := db.GetMap[string, string](reqMetaDataKey)
	if !suc {
		reqMetaData = make(map[string]string)
	}
	reqMetaData[tgf.ContextKeyUserId] = userId
	reqMetaData[tgf.ContextKeyNodeId] = tgf.NodeId
	ct.SetValue(share.ReqMetaDataKey, reqMetaData)
	t.users.Set(userId, userData)
	userData.Login(userId)
	//remove key
	t.users.Del(templateUserId)
	log.InfoTag("tcp", "login templateUserId %v , uuid %v", templateUserId, userId)
	if t.userHook != nil {
		for _, hook := range t.userHook.GetLoginHooks() {
			SendNoReplyRPCMessage(ct, hook.New(&DefaultArgs{C: userId}, &EmptyReply{}))
		}
	}
	return
}

func (t *TCPServer) doLogic(data *RequestData) {
	var (
		err       error
		startTime = time.Now().UnixMilli()
	)
	reply := make([]byte, 0)

	reqData := &Args[protoiface.MessageV1]{}
	reqData.ByteData = data.Data

	resData := &Reply[protoiface.MessageV1]{}
	callback, err := sendMessage(data.User, data.Module, data.RequestMethod, reqData, resData)
	if err != nil {
		log.InfoTag("tcp", "请求异常 数据 [%v] [%v]", data, err)
		return
	}
	callbackErr := callback.Done()
	if callbackErr != nil {
		log.InfoTag("tcp", "请求异常 数据 [%v] [%v]", data, callbackErr)
		return
	}
	reply = resData.ByteData
	consumeTime := time.Now().UnixMilli() - startTime
	log.DebugTag("tcp", "请求耗时统计 module=%v serviceName=%v consumeTime=%v", data.Module, data.RequestMethod, consumeTime)
	clientData := t.getSendToClientData(data.Module+"."+data.RequestMethod, data.ReqId, resData.Code, reply)
	data.User.Send(clientData)
}

func (t *TCPServer) getSendToClientData(messageType string, reqId, code int32, reply []byte) (res []byte) {
	var (
		compress byte = 0
		err      error
	)
	bp := bytebufferpool.Get()
	// [1][1][2][4][n][n]
	// message type|compress|request method name size|data size|method name|data
	//逻辑响应
	bp.WriteByte(byte(Logic))
	if t.config.IsWebSocket() {
		data := &WSResponse{}
		data.MessageType = messageType
		data.Data = reply
		data.ReqId = reqId
		data.Code = code
		b, _ := proto.Marshal(data)
		bp.Write(b)
		res = bp.Bytes()
		return
	}

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
		t.userHook = t.config.UserHook()
		if t.config.IsWebSocket() {
			util.Go(func() {
				log.InfoTag("init", "启动ws服务 %v", t.config.Address()+":"+t.config.Port()+"/"+t.config.WsPath())
				// 定义 WebSocket 路由
				http.HandleFunc("/"+t.config.WsPath(), t.wsHandler)
				// 启动服务器
				err := http.ListenAndServe(t.config.Address()+":"+t.config.Port(), nil)
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
	var ()
	// 将 HTTP 连接升级为 WebSocket 连接
	conn, err := upGrader.Upgrade(w, r, nil)
	if err != nil {
		log.Info("%v", err)
		return
	}
	util.Go(func() {
		t.handlerWSConn(conn)
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

func (t *TCPServer) ToUser(userId, messageType string, data []byte) {
	var ()
	if connectData, ok := t.users.Get(userId); ok {
		res := t.getSendToClientData(messageType, 0, 0, data)
		connectData.Send(res)
	} else {
		log.DebugTag("tcp", "userid=%v user connection not found", userId)
	}
}

func (u *UserConnectData) UpdateUserNodeId(servicePath, nodeId string) {
	var ()
	metaData := u.contextData.Value(share.ReqMetaDataKey)
	if metaData != nil {
		metaData.(map[string]string)[servicePath] = nodeId
	}
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
func (u *UserConnectData) Offline(userHook IUserHook) {
	defer func() {
		if r := recover(); r != nil {
			log.DebugTag("tcp", "用户 userId=%v Offline: %v", u.userId, r)
		}
	}()
	var ()
	if userHook != nil {
		for _, hook := range userHook.GetOfflineHooks() {
			SendNoReplyRPCMessage(u.contextData, hook.New(&DefaultArgs{C: u.userId}, &EmptyReply{}))
		}
	}

	u.contextData.Deadline()
	ip := ""
	if u.conn != nil {
		u.conn.Close()
		ip = u.conn.RemoteAddr().String()
	}
	if u.wsConn != nil {
		u.wsConn.Close()
		ip = u.wsConn.RemoteAddr().String()
	}
	log.DebugTag("tcp", "用户 userId=%v 离线 ip=%v", u.userId, ip)
	u.stop <- struct{}{}
}

func (u *UserConnectData) Stop() {
	var ()
	u.stop <- struct{}{}
}
func (u *UserConnectData) Login(userId string) {
	var ()
	u.userId = userId
}
func (u *UserConnectData) Send(data []byte) {
	var ()
	defer func() {
		if e := recover(); e != nil {
			log.WarnTag("tcp", "发送请求异常")
		}
	}()
	if u.conn != nil {
		u.conn.Write(data)

	} else if u.wsConn != nil {
		u.wsConn.WriteMessage(websocket.BinaryMessage, data)
	} else {
		log.DebugTag("tcp", "用户没有可用的连接数据 %v", u.userId)
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
