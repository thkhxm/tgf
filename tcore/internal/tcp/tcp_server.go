package tcp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"tframework.com/rpc/tcore/internal/plugin"
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
// [1][1][2][1][1][2][n][n]
// magic number|message type|request method name size|data size|method name|data
type RequestHeader []byte

// ResponseHeader
// [1][1][4]
// message type|compress|size|
type ResponseHeader []byte

type HeaderMessageType byte

//***********************    type_end    ****************************

//***********************    var    ****************************

var requestHeadSize = 8

// 最大同时连接数
var maxSynChanConn = 3000

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
}

type ServerConfig struct {
	MaxConnections int32 //最大连接数
	DeadLineTime   time.Duration
}

type UserConnectData struct {
	conn     *net.TCPConn
	reqCount int32
	reader   *bufio.Reader
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
		tcp.SetDeadline(time.Now().Add(time.Second * this.config.DeadLineTime))
		this.conChan <- tcp //将链接放入管道中
	}
}

func (this *Server) onDestroy() {
	this.closeChan <- true
}

func (this *Server) handlerConn(conn *net.TCPConn) {
	defer func() {
		if err := recover(); err != nil {
			plugin.InfoS("[tcp] tcp连接异常关闭 %v", err)
		}
		conn.Close()
	}()
	connectData := &UserConnectData{
		conn:     conn,
		reqCount: 0,
		reader:   bufio.NewReader(conn),
	}

	for {
		//read head
		// RequestHeader
		// [1][1][2][1][1][2][n][n]
		// magic number|message type|request method name size|data size|method name|data

		if connectData.reader.Buffered() < 2 {
			plugin.InfoS("[tcp] 请求头长度不足2 重新等待接收数据")
			continue
		}

		head2, err := connectData.reader.Peek(2)
		if err != nil && err != io.EOF {
			plugin.InfoS("[tcp] 请求头读取前两个字节数据异常,强制断开连接 %v", err)
			break
		}

		//请求魔法值
		magicNumber := head2[0]
		if magicNumber != requestMagicNumber {
			plugin.InfoS("[tcp] 请求头magic number错误,强制断开连接 %v", err)
			break
		}
		//请求消息类型
		messageType := head2[1]
		switch messageType {
		case byte(Heartbeat):
			connectData.reader.Discard(2)
			connectData.conn.SetDeadline(time.Now().Add(time.Second * this.config.DeadLineTime))
			continue
		case byte(Logic):
		default:
			er := errors.New(fmt.Sprintf("message type error %v", messageType))
			panic(er)
		}

		if connectData.reader.Buffered() < 10 {
			//长度不足 读取失败 计数
			continue
		}
		connectData.reqCount++
	}
}

func NewDefaultServerConfig() *ServerConfig {
	config := &ServerConfig{MaxConnections: 10000, DeadLineTime: 30}
	return config
}
