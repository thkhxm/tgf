package tcp

import (
	"net"
	"tframework.com/rpc/tcore/internal/plugin"
)

//***************************************************
//author tim.huang
//2022/12/6
//
//
//***************************************************

//***********************    type    ****************************

// RequestHeader
// [1][1]
// magic number|message type|
type RequestHeader []byte

// ResponseHeader
// [1][1][4]
// message type|compress|size|
type ResponseHeader []byte

//***********************    type_end    ****************************

//***********************    var    ****************************

// 最大同时连接数
var maxSynChanConn = 3000

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
}

type ServerConfig struct {
	MaxConnections int32 //最大连接数
}

type UserConnectData struct {
	conn *net.TCPConn
}

//***********************    struct_end    ****************************

func (this *Server) InitStruct(config *ServerConfig) {
	if config == nil {
		this.config = NewDefaultServerConfig()
	} else {
		this.config = config
	}
	//
	this.conChan = make(chan *net.TCPConn, maxSynChanConn)
	this.closeChan = make(chan bool, 1)

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
	add, _ := net.ResolveTCPAddr("tcp", "192.168.1.90:8880")
	listen, err := net.ListenTCP("tcp", add)
	if err != nil {
		plugin.InfoS("[tcp] tcp服务 启动异常 %v", err)
		return
	}
	for {
		tcp, _ := listen.AcceptTCP()
		tcp.SetNoDelay(true)         //无延迟
		tcp.SetKeepAlive(true)       //保持激活
		tcp.SetReadBuffer(1024)      //设置读缓冲区大小
		tcp.SetWriteBuffer(8 * 1024) //设置写缓冲区大小
		this.conChan <- tcp
	}
}

func (this *Server) onDestroy() {
	this.closeChan <- true
}

func (this *Server) handlerConn(conn *net.TCPConn) {

}

func NewDefaultServerConfig() *ServerConfig {
	config := &ServerConfig{MaxConnections: 10000}
	return config
}
