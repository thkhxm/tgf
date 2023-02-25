package rpc

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/cornelk/hashmap"
	client2 "github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/share"
	util2 "github.com/smallnest/rpcx/util"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc/internal"
	"github.com/thkhxm/tgf/util"
	"github.com/valyala/bytebufferpool"
	"golang.org/x/net/context"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
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
)

const (
	Heartbeat HeaderMessageType = iota
	Logic
	Login
)

const (
	defaultLazyInitRPC   = true
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
}

type TCPServer struct {
	config    *ServerConfig     //tcp连接配置
	conChan   chan *net.TCPConn //客户端连接chan
	closeChan chan bool         //关闭chan
	clients   *hashmap.Map[string, client2.XClient]
	users     *hashmap.Map[string, IUserConnectData]
	optionals []optional
	//
	//是否采用懒加载,rpc的连接.默认为true
	lazyInitRPC bool
	loginCheck  internal.ILoginCheck
	startup     *sync.Once //是否已经启动
}

type optional func(server *TCPServer)

type ServerConfig struct {
	address         string //地址
	port            string //端口
	maxConnections  int32  //最大连接数
	deadLineTime    time.Duration
	readBufferSize  int
	writeBufferSize int
}

type UserConnectData struct {
	conn        *net.TCPConn
	reqCount    int32
	reader      *bufio.Reader
	userId      string
	contextData context.Context
}

type RequestData struct {
	User          *UserConnectData
	RequestMethod string
	Module        string
	Data          []byte
	MessageType   HeaderMessageType
}

func (this *TCPServer) selectorChan() {
	for {
		select {
		case con := <-this.conChan:
			util.Go(func() {
				this.handlerConn(con)
			})
		}
	}
}
func (this *TCPServer) onDestroy() {
	this.closeChan <- true
}
func (this *TCPServer) handlerConn(conn *net.TCPConn) {
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
	}
	log.Debug("[tcp] 接收到一条新的连接 addr=%v ", conn.RemoteAddr().String())
	//
	stop := make(chan struct{})
	reqChan := make(chan *RequestData)
	defer func() {
		if err := recover(); err != nil {
			log.Debug("[tcp] tcp连接异常关闭 %v", err)
		}
		stop <- struct{}{}
		conn.Close()
	}()
	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Debug("[tcp] 业务逻辑chan关闭 %v", err)
			}
		}()
		for {
			select {
			case req := <-reqChan:
				this.doLogic(req)
			case <-stop:
				close(stop)
				close(reqChan)
				return
			}
		}
	}()
	for {
		head, err = connectData.reader.Peek(2)
		if err != nil && err != io.EOF {
			log.Debug("[tcp] 请求头读取数据异常,强制断开连接 %v", err)
			break
		}

		//请求魔法值
		magicNumber := head[0]
		if magicNumber != requestMagicNumber {
			log.Debug("[tcp] 请求头magic number错误,强制断开连接 %v", err)
			break
		}
		//请求消息类型
		messageType := head[1]
		switch messageType {

		case byte(Heartbeat): //处理心跳逻辑
			connectData.reader.Discard(2)
			connectData.conn.SetDeadline(time.Now().Add(time.Second * this.config.deadLineTime))
			connectData.conn.Write([]byte{byte(Heartbeat)})
			continue
		case byte(Login):
			//	[1][1][2][n]
			//magic number|message type|data size|data
			head, err = connectData.reader.Peek(int(requestLoginHeadSize))
			if err != nil {
				log.Debug("[tcp] Login 请求头数据异常,强制断开连接 %v", err)
				break
			}
			dataSize = binary.BigEndian.Uint16(head[2:4])
			totalLen := requestLoginHeadSize + dataSize
			if connectData.reader.Buffered() < int(totalLen) {
				log.Debug("[tcp] Login 包长度不足，重新读取等待长度足够 %v--%v", connectData.reader.Buffered(), totalLen)
				continue
			}
			allData := make(RequestHeader, totalLen)
			rdLen, err = connectData.reader.Read(allData)
			data := allData[requestLoginHeadSize:]
			//
			this.doLogin(connectData.contextData.(*share.Context), string(data))
		case byte(Logic): //处理请求业务逻辑
			head, err = connectData.reader.Peek(int(requestHeadSize))
			if err != nil {
				log.Debug("[tcp] Logic 请求头数据异常,强制断开连接 %v", err)
				break
			}
			//	[1][1][2][2][n][n]
			//magic number|message type|request method name size|data size|method name|data
			methodSize = binary.BigEndian.Uint16(head[2:4])
			dataSize = binary.BigEndian.Uint16(head[4:6])
			//算出完整包长度
			totalLen := requestHeadSize + methodSize + dataSize
			if connectData.reader.Buffered() < int(totalLen) {
				log.Debug("[tcp] Logic 包长度不足，重新读取等待长度足够 %v--%v", connectData.reader.Buffered(), totalLen)
				continue
			}
			allData := make(RequestHeader, totalLen)
			rdLen, err = connectData.reader.Read(allData)
			if rdLen != int(totalLen) || err != nil {
				log.Debug("[tcp] Logic 包长度不足 有异常 %v--%v", connectData.reader.Buffered(), totalLen)
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
			log.Debug("[tcp] Logic 完整包数据 [%v]", pack)
			reqChan <- pack
		default:
			er := errors.New(fmt.Sprintf("message type error %v", messageType))
			panic(er)
		}
		connectData.reqCount++
	}
}

func (this *TCPServer) doLogin(ct *share.Context, token string) {
	var (
		key, uuid, reqMetaDataKey string
		ok                        bool
	)

	// TODO 通过jwt验证token有效性
	//通过token,获取到uuid
	if ok, uuid = this.loginCheck.CheckLogin(token); !ok {
		log.Warn("[tcp] login failed token %v , uuid %v", token, uuid)
		return
	}

	//判断当前uuid的token是否一致
	key = fmt.Sprintf(tgf.RedisKeyUserLoginToken, uuid)
	curToken := db.Get[string](key)
	//token不一致,拒绝登录,用户刷新token,广播网关协议,移除旧token的用户连接
	if token != curToken {
		// TODO 重复登录,踢出之前登录的用户
	}
	db.Set(key, token, loginTokenTimeOut)
	ct.SetValue(tgf.ContextKeyUserId, uuid)
	//
	reqMetaDataKey = fmt.Sprintf(tgf.RedisKeyUserNodeMeta, uuid)
	reqMetaData := db.GetMap[string, string](reqMetaDataKey)
	reqMetaData[tgf.ContextKeyUserId] = uuid
	ct.SetValue(share.ReqMetaDataKey, reqMetaData)
	log.Info("[tcp] login token %v , uuid %v", token, uuid)
}
func (this *TCPServer) doLogic(data *RequestData) {
	var (
		compress byte = 0
		err      error
	)
	reply := make([]byte, 0)
	callback, _ := SendRPCMessage(data.Module, data.RequestMethod, data.Data, &reply)
	if err != nil {
		log.Info("[tcp] 请求异常 数据 [%v] [%v]", data, err)
		return
	}
	<-callback.Done
	if callback.Error != nil {
		log.Info("[tcp] 请求异常 数据 [%v] [%v]", data, callback.Error)
		return
	}
	log.Info("[tcp] 请求数据 [%v]", reply)
	bp := bytebufferpool.Get()
	// [1][1][2][4][n][n]
	// message type|compress|request method name size|data size|method name|data

	//逻辑响应
	bp.WriteByte(byte(Logic))

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
			log.Info("[tcp] 数据压缩异常 [%v] 压缩数据 [%v] [%v]", data, reply, err)
			return
		}
	}
	bp.Write(reply)
	data.User.conn.Write(bp.Bytes())
}

func (this *TCPServer) WithPort(port string) *TCPServer {
	var ()
	this.optionals = append(this.optionals, func(server *TCPServer) {
		server.config.port = port
	})
	return this
}
func (this *TCPServer) WithBuffer(readBuffer, writeBuffer int) {
	var ()
	this.optionals = append(this.optionals, func(server *TCPServer) {
		server.config.readBufferSize = readBuffer
		server.config.writeBufferSize = writeBuffer
	})

}
func (this *TCPServer) WithLoginCheck(f internal.ILoginCheck) {
	var ()
	this.optionals = append(this.optionals, func(server *TCPServer) {
		server.loginCheck = f
	})
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

func (this *TCPServer) Run() {
	var ()
	//保证每个tcp只会被启动一次,避免误操作
	this.startup.Do(func() {
		//执行optional
		for _, optional := range this.optionals {
			optional(this)
		}
		//
		add, _ := net.ResolveTCPAddr("tcp", fmt.Sprintf("%v:%v", this.config.address, this.config.port))
		listen, err := net.ListenTCP("tcp", add)
		if err != nil {
			log.Info("[init] tcp服务 启动异常 %v", err)
			return
		}
		log.Info("[init] tcp服务 启动成功 %v", add)

		//启动selector线程，等待连接接入
		util.Go(func() {
			log.Info("[init] tcp selector 启动成功")
			this.selectorChan()
		})
		for {
			tcp, _ := listen.AcceptTCP()
			tcp.SetNoDelay(true)                            //无延迟
			tcp.SetKeepAlive(true)                          //保持激活
			tcp.SetReadBuffer(this.config.readBufferSize)   //设置读缓冲区大小
			tcp.SetWriteBuffer(this.config.writeBufferSize) //设置写缓冲区大小
			tcp.SetDeadline(time.Now().Add(this.config.deadLineTime))
			this.conChan <- tcp //将链接放入管道中
		}
	})
}

func newDefaultServerConfig(port string) *ServerConfig {
	serverConfig := &ServerConfig{
		address:         defaultIp,
		port:            port,
		readBufferSize:  defaultReadBuffer,
		writeBufferSize: defaultWriteBuffer,
		deadLineTime:    defaultDeadLineTime,
		maxConnections:  defaultMaxConnections,
	}
	return serverConfig
}

func NewDefaultTCPServer() *TCPServer {
	server := &TCPServer{}
	server.optionals = make([]optional, 0)
	server.lazyInitRPC = defaultLazyInitRPC
	server.config = newDefaultServerConfig(defaultTcpServerPort)
	server.clients = hashmap.New[string, client2.XClient]()
	server.conChan = make(chan *net.TCPConn, maxSynChanConn)
	server.closeChan = make(chan bool, 1)
	server.startup = new(sync.Once)
	server.loginCheck = &internal.LoginCheck{}
	return server
}
