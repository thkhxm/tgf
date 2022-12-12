package tcp

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"tframework.com/rpc/tcore/internal/plugin"
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

type RequestData struct {
	RequestMethod string
	Module        string
	Data          []byte
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

		//if connectData.reader.Buffered() < 2 {
		//	plugin.InfoS("[tcp] 请求头长度不足2 重新等待接收数据")
		//	continue
		//}

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
			allData := make([]byte, totalLen)
			rdLen, err = connectData.reader.Read(allData)
			if rdLen != int(totalLen) || err != nil {
				plugin.InfoS("[tcp] 包长度不足 有异常 %v--%v", connectData.reader.Buffered(), totalLen)
				continue
			}
			reqNameIndex := methodSize + 6 + 1
			reqName := utils.ConvertStringByByteSlice(allData[6:reqNameIndex])
			ix := strings.LastIndex(reqName, ".")
			reqModule := reqName[ix+1:]
			pack := &RequestData{
				RequestMethod: reqName,
				Module:        reqModule,
				Data:          allData[reqNameIndex:],
			}
			plugin.InfoS("[tcp] 完整包数据 [%v]", pack)
		default:
			er := errors.New(fmt.Sprintf("message type error %v", messageType))
			panic(er)
		}
		connectData.reqCount++
	}
}

func NewDefaultServerConfig() *ServerConfig {
	config := &ServerConfig{MaxConnections: 10000, DeadLineTime: 30}
	return config
}

func NewDefaultTCPServer() *Server {
	server := &Server{}
	server.InitStruct(NewDefaultServerConfig())
	return server
}
