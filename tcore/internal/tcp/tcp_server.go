package tcp

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	client2 "github.com/smallnest/rpcx/client"
	"golang.org/x/net/context"
	"io"
	"net"
	"strings"
	"sync"
	"tframework.com/rpc/tcore/config"
	"tframework.com/rpc/tcore/internal/plugin"
	tserver "tframework.com/rpc/tcore/internal/server"
	"tframework.com/rpc/tcore/utils"
	"time"
)

//***************************************************
//author tim.huang
//2022/12/6
//
//
//***************************************************

//***********************    type    ****************************

// RequestHeader
// [1][1][2][2][n][n]
// magic number|message type|request method name size|data size|method name|data
type RequestHeader []byte

// ResponseHeader
// [1][1][4]
// message type|compress|size|
type ResponseHeader []byte

type HeaderMessageType byte

//***********************    type_end    ****************************

//***********************    var    ****************************

var requestHeadSize uint16 = 6

// 最大同时连接数
var maxSynChanConn = 3000

// 连接超时时间
var deadLineTime time.Duration = 30

var requestMagicNumber byte = 250

const (
	Heartbeat HeaderMessageType = iota
	Logic
)

//***********************    var_end    ****************************

//***********************    interface    ****************************

type ITCPServer interface {
	Start()
	Send()
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

type Server struct {
	config    *ServerConfig     //tcp连接配置
	conChan   chan *net.TCPConn //客户端连接chan
	closeChan chan bool         //关闭chan
	clients   *sync.Map
}

type ServerConfig struct {
	Address        string //地址
	Port           int    //端口
	MaxConnections int32  //最大连接数
	DeadLineTime   time.Duration
}

type UserConnectData struct {
	conn     *net.TCPConn
	reqCount int32
	reader   *bufio.Reader
	reqChan  chan *RequestData
}

type RequestData struct {
	RequestMethod string
	Module        string
	Data          []byte
}

//***********************    struct_end    ****************************

func (this *Server) InitPlugin() {

}

func (this *Server) StartPlugin() {

}

func (this *Server) initStruct(config *ServerConfig) {
	this.config = config
	//
	this.conChan = make(chan *net.TCPConn, maxSynChanConn)
	this.closeChan = make(chan bool, 1)
	this.clients = new(sync.Map)

	go this.selectorChan()
}

func (this *Server) selectorChan() {
	for {
		select {
		case con := <-this.conChan:
			go this.handlerConn(con)
		}
	}
}

func (this *Server) Start() {
	add, _ := net.ResolveTCPAddr("tcp", fmt.Sprintf("%v:%v", this.config.Address, this.config.Port))
	listen, err := net.ListenTCP("tcp", add)
	if err != nil {
		plugin.InfoS("[tcp] tcp服务 启动异常 %v", err)
		return
	}
	plugin.InfoS("[tcp] tcp服务 启动成功 %v", add)
	for {
		tcp, _ := listen.AcceptTCP()
		tcp.SetNoDelay(true)         //无延迟
		tcp.SetKeepAlive(true)       //保持激活
		tcp.SetReadBuffer(1024)      //设置读缓冲区大小
		tcp.SetWriteBuffer(8 * 1024) //设置写缓冲区大小
		tcp.SetDeadline(time.Now().Add(time.Second * this.config.DeadLineTime))
		this.conChan <- tcp //将链接放入管道中
	}
}

func (this *Server) onDestroy() {
	this.closeChan <- true
}

func (this *Server) handlerConn(conn *net.TCPConn) {

	var (
		err                  error
		head                 []byte
		methodSize, dataSize uint16
		rdLen                int
	)
	connectData := &UserConnectData{
		conn:     conn,
		reqCount: 0,
		reader:   bufio.NewReader(conn),
		reqChan:  make(chan *RequestData),
	}

	defer func() {
		if err := recover(); err != nil {
			plugin.InfoS("[tcp] tcp连接异常关闭 %v", err)
		}
		conn.Close()
		close(connectData.reqChan)
	}()

	go func() {
		defer func() {
			if err := recover(); err != nil {
				plugin.InfoS("[tcp] 业务逻辑chan关闭 %v", err)
			}
			conn.Close()
			close(connectData.reqChan)
		}()
		for {
			select {
			case req := <-connectData.reqChan:
				this.DoLogic(req)
			}
		}
	}()
	for {
		head, err = connectData.reader.Peek(2)
		if err != nil && err != io.EOF {
			plugin.InfoS("[tcp] 请求头读取前两个字节数据异常,强制断开连接 %v", err)
			break
		}

		//请求魔法值
		magicNumber := head[0]
		if magicNumber != requestMagicNumber {
			plugin.InfoS("[tcp] 请求头magic number错误,强制断开连接 %v", err)
			break
		}
		//请求消息类型
		messageType := head[1]
		switch messageType {

		case byte(Heartbeat): //处理心跳逻辑
			connectData.reader.Discard(2)
			connectData.conn.SetDeadline(time.Now().Add(time.Second * this.config.DeadLineTime))
			continue
		case byte(Logic): //处理请求业务逻辑
			head, err = connectData.reader.Peek(int(requestHeadSize))
			if err != nil {
				plugin.InfoS("[tcp] 请求头数据异常,强制断开连接 %v", err)
				break
			}
			//	[1][1][2][2][n][n]
			//magic number|message type|request method name size|data size|method name|data
			methodSize = binary.BigEndian.Uint16(head[2:4])
			dataSize = binary.BigEndian.Uint16(head[4:6])
			//算出完整包长度
			totalLen := requestHeadSize + methodSize + dataSize
			if connectData.reader.Buffered() < int(totalLen) {
				plugin.InfoS("[tcp] 包长度不足，重新读取等待长度足够 %v--%v", connectData.reader.Buffered(), totalLen)
				continue
			}
			allData := make(RequestHeader, totalLen)
			rdLen, err = connectData.reader.Read(allData)
			if rdLen != int(totalLen) || err != nil {
				plugin.InfoS("[tcp] 包长度不足 有异常 %v--%v", connectData.reader.Buffered(), totalLen)
				continue
			}
			reqNameIndex := methodSize + 6 + 1
			reqName := utils.ConvertStringByByteSlice(allData[6:reqNameIndex])
			ix := strings.LastIndex(reqName, ".")
			reqMethodName := reqName[ix+1:]
			reqModule := reqName[:ix]
			pack := &RequestData{
				RequestMethod: reqMethodName,
				Module:        reqModule,
				Data:          allData[reqNameIndex:],
			}
			plugin.InfoS("[tcp] 完整包数据 [%v]", pack)
			connectData.reqChan <- pack
		default:
			er := errors.New(fmt.Sprintf("message type error %v", messageType))
			panic(er)
		}
		connectData.reqCount++
	}
}

func (this *Server) DoLogic(data *RequestData) {
	client := this.GetClient(data.Module)
	reply := make([]byte, 0)
	err := client.Call(context.Background(), data.RequestMethod, data.Data, reply)
	if err != nil {
		plugin.InfoS("[tcp] 请求异常 数据 [%v]", data, err)
		return
	}
	plugin.InfoS("[tcp] 请求数据 [%v]", reply)
}

func (this *Server) GetClient(moduleName string) client2.XClient {
	if val, ok := this.clients.Load(moduleName); ok {
		return val.(client2.XClient)
	}
	val := tserver.ConsulDiscovery.RegisterTCPService(moduleName)
	this.clients.Store(moduleName, val)
	return val
}

func NewDefaultServerConfig() *ServerConfig {
	serverConfig := &ServerConfig{
		Address:        "0.0.0.0",
		Port:           8880,
		DeadLineTime:   300,
		MaxConnections: 10000,
	}
	return serverConfig
}

func NewDefaultTCPServer(config *config.TCPServerConfig) *Server {
	if config == nil {
		panic(errors.New("[server] 缺少TCP配置"))
	}
	server := &Server{}
	serverConfig := &ServerConfig{
		Address:        config.Address,
		Port:           config.Port,
		MaxConnections: 10000,
		DeadLineTime:   config.DeadLineTime,
	}
	server.initStruct(serverConfig)
	return server
}

func NewDefaultTCPServerTest() *Server {
	server := &Server{}
	server.initStruct(NewDefaultServerConfig())
	return server
}
