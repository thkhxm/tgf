package tcp

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/edwingeng/doublejump"
	client2 "github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/share"
	"github.com/smallnest/rpcx/util"
	"github.com/valyala/bytebufferpool"
	"golang.org/x/net/context"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"tframework.com/rpc/tcore"
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/internal/define"
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
// [1][1][2][4][n][n]
// message type|compress|request method name size|data size|method name|data
type ResponseHeader []byte

type HeaderMessageType byte

//***********************    type_end    ****************************

//***********************    var    ****************************

var requestHeadSize uint16 = 6
var requestLoginHeadSize uint16 = 4

// 最大同时连接数
var maxSynChanConn = 3000

// 连接超时时间
var deadLineTime time.Duration = 30

var requestMagicNumber byte = 250

// 最低压缩大小
var compressMinSize = 1024 * 8

const (
	Heartbeat HeaderMessageType = iota
	Logic
	Login
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
	users     *sync.Map
	service   tframework.ITCPService
}

type CustomSelector struct {
	h       *doublejump.Hash
	servers []string
}

type ServerConfig struct {
	Address        string //地址
	Port           int    //端口
	MaxConnections int32  //最大连接数
	DeadLineTime   time.Duration
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

//***********************    struct_end    ****************************

func (this *Server) InitPlugin() {

}

func (this *Server) StartPlugin() {

}

func (this *Server) initStruct(config *ServerConfig, service tframework.ITCPService) {
	this.config = config
	//
	this.conChan = make(chan *net.TCPConn, maxSynChanConn)
	this.closeChan = make(chan bool, 1)
	this.clients = new(sync.Map)
	this.users = new(sync.Map)
	this.service = service
	go this.selectorChan()
}

func (this *Server) selectorChan() {
	for {
		select {
		case con := <-this.conChan:
			//TODO 管理一个线程池
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
		conn:        conn,
		reqCount:    0,
		reader:      bufio.NewReader(conn),
		contextData: share.NewContext(context.Background()),
	}
	stop := make(chan struct{})
	reqChan := make(chan *RequestData)
	defer func() {
		if err := recover(); err != nil {
			plugin.InfoS("[tcp] tcp连接异常关闭 %v", err)
		}
		stop <- struct{}{}
		conn.Close()
	}()
	go func() {
		defer func() {
			if err := recover(); err != nil {
				plugin.InfoS("[tcp] 业务逻辑chan关闭 %v", err)
			}
		}()
		for {
			select {
			case req := <-reqChan:
				this.DoLogic(req)
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
			plugin.InfoS("[tcp] 请求头读取数据异常,强制断开连接 %v", err)
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
			connectData.conn.Write([]byte{byte(Heartbeat)})
			continue
		case byte(Login):
			//	[1][1][2][n]
			//magic number|message type|data size|data
			head, err = connectData.reader.Peek(int(requestLoginHeadSize))
			if err != nil {
				plugin.InfoS("[tcp] Login 请求头数据异常,强制断开连接 %v", err)
				break
			}
			dataSize = binary.BigEndian.Uint16(head[2:4])
			totalLen := requestLoginHeadSize + dataSize
			if connectData.reader.Buffered() < int(totalLen) {
				plugin.InfoS("[tcp] Login 包长度不足，重新读取等待长度足够 %v--%v", connectData.reader.Buffered(), totalLen)
				continue
			}
			allData := make(RequestHeader, totalLen)
			rdLen, err = connectData.reader.Read(allData)
			data := allData[requestLoginHeadSize:]
			//
			this.service.Login(connectData.contextData.(*share.Context), string(data))

		case byte(Logic): //处理请求业务逻辑
			head, err = connectData.reader.Peek(int(requestHeadSize))
			if err != nil {
				plugin.InfoS("[tcp] Logic 请求头数据异常,强制断开连接 %v", err)
				break
			}
			//	[1][1][2][2][n][n]
			//magic number|message type|request method name size|data size|method name|data
			methodSize = binary.BigEndian.Uint16(head[2:4])
			dataSize = binary.BigEndian.Uint16(head[4:6])
			//算出完整包长度
			totalLen := requestHeadSize + methodSize + dataSize
			if connectData.reader.Buffered() < int(totalLen) {
				plugin.InfoS("[tcp] Logic 包长度不足，重新读取等待长度足够 %v--%v", connectData.reader.Buffered(), totalLen)
				continue
			}
			allData := make(RequestHeader, totalLen)
			rdLen, err = connectData.reader.Read(allData)
			if rdLen != int(totalLen) || err != nil {
				plugin.InfoS("[tcp] Logic 包长度不足 有异常 %v--%v", connectData.reader.Buffered(), totalLen)
				continue
			}
			reqNameIndex := methodSize + 6
			reqName := utils.ConvertStringByByteSlice(allData[6:reqNameIndex])
			ix := strings.LastIndex(reqName, ".")
			reqMethodName := reqName[ix+1:]
			reqModule := reqName[:ix]
			pack := &RequestData{
				RequestMethod: reqMethodName,
				Module:        reqModule,
				Data:          allData[reqNameIndex:],
				User:          connectData,
			}
			plugin.InfoS("[tcp] Logic 完整包数据 [%v]", pack)
			reqChan <- pack
		default:
			er := errors.New(fmt.Sprintf("message type error %v", messageType))
			panic(er)
		}
		connectData.reqCount++
	}
}

func (this *Server) DoLogic(data *RequestData) {
	var (
		compress byte = 0
		err      error
	)
	client := this.GetClient(data.Module)
	reply := make([]byte, 0)
	done := make(chan *client2.Call, 10)
	_, err = client.Go(data.User.contextData, data.RequestMethod, data.Data, &reply, done)
	if err != nil {
		plugin.InfoS("[tcp] 请求异常 数据 [%v] [%v]", data, err)
		return
	}
	callback := <-done
	if callback.Error != nil {
		plugin.InfoS("[tcp] 请求异常 数据 [%v] [%v]", data, callback.Error)
		return
	}
	plugin.InfoS("[tcp] 请求数据 [%v]", reply)
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
		reply, err = util.Zip(reply)
		if err != nil {
			plugin.InfoS("[tcp] 数据压缩异常 [%v] 压缩数据 [%v] [%v]", data, reply, err)
			return
		}
	}
	bp.Write(reply)
	data.User.conn.Write(bp.Bytes())
}

func (this *Server) GetClient(moduleName string) client2.XClient {
	if val, ok := this.clients.Load(moduleName); ok {
		return val.(client2.XClient)
	}
	val := tserver.ConsulDiscovery.RegisterTCPService(moduleName)
	selector := new(CustomSelector)
	selector.initStruct()
	val.SetSelector(selector)
	val.GetPlugins().Add(this)
	this.clients.Store(moduleName, val)
	return val
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
func (this *Server) PostCall(ctx context.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, err error) error {
	return nil
}

func (this *CustomSelector) Select(ctx context.Context, servicePath, serviceMethod string, args interface{}) string {
	if sc, ok := ctx.(*share.Context); ok {
		size := len(this.servers)
		switch size {
		case 0:
			return ""
		default:
			reqMetaData := sc.Value(share.ReqMetaDataKey).(map[string]string)
			//用户级别的请求
			uid := reqMetaData[tframework.ContextKey_UserId]
			if uid != "" {
				//判断之前的节点是否存活,如果存活,直接命中
				selected := reqMetaData[servicePath]
				if selected != "" && this.checkServerAlive(selected) {
					return selected
				}
				//通过一致性hash的方式,命中一个活跃的业务节点
				key := client2.HashString(uid)
				selected, _ = this.h.Get(key).(string)
				reqMetaData[servicePath] = selected
				reqMetaDataKey := fmt.Sprintf(define.User_NodeMeta_RedisKey, uid)
				tcore.Redis.PutMapFiled(reqMetaDataKey, servicePath, selected, time.Hour*24*3)
				return selected
			}
		}
	}

	return ""
}
func (this *CustomSelector) checkServerAlive(server string) bool {
	var ()
	for _, s := range this.servers {
		if s == server {
			return true
		}
	}
	return false
}
func (this *CustomSelector) UpdateServer(servers map[string]string) {
	// TODO: 新增虚拟节点，优化hash的命中分布
	ss := make([]string, 0, len(servers))
	for k := range servers {
		this.h.Add(k)
		ss = append(ss, k)
	}

	sort.Slice(ss, func(i, j int) bool { return ss[i] < ss[j] })

	for _, k := range this.servers {
		if servers[k] == "" { // remove
			this.h.Remove(k)
		}
	}
	this.servers = ss
	plugin.InfoS("更新服务节点%v", this.servers)
}

// s
func (this *CustomSelector) initStruct() {
	this.servers = make([]string, 0, 0)
	this.h = doublejump.NewHash()
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

func NewDefaultTCPServer(config *config.TCPServerConfig, service tframework.ITCPService) *Server {
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

	server.initStruct(serverConfig, service)
	return server
}
