package rpc

import (
	"context"
	"errors"
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/golang/protobuf/proto"
	client2 "github.com/rpcxio/rpcx-consul/client"
	"github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/server"
	"github.com/smallnest/rpcx/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc/internal"
	"github.com/thkhxm/tgf/util"
	"math/rand"
	"os"
	"os/signal"
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
//2023/2/23
//***************************************************

const (
	defaultMaxWorkers  = 1e4
	defaultMaxCapacity = 1e4
)

var singletonLock = &sync.Mutex{}

// Server
// @Description:
type Server struct {
	rpcServer *server.Server

	//启动后执行的操作
	afterOptionals []Optional
	//启动前执行
	beforeOptionals []Optional
	//
	maxWorkers  int
	maxCapacity int
	//
	service   []IService
	closeChan chan bool
	//
	minPort int32
	maxPort int32
	//
}

type Optional func(*Server)

func (this *Server) withConsulDiscovery() *Server {
	var ()
	this.beforeOptionals = append(this.beforeOptionals, func(server *Server) {
		internal.UseConsulDiscovery()
	})
	return this
}

func (this *Server) WithServerPool(maxWorkers, maxCapacity int) *Server {
	this.beforeOptionals = append(this.beforeOptionals, func(server *Server) {
		server.maxWorkers = maxWorkers
		server.maxCapacity = maxCapacity
		log.InfoTag("init", "修改rpcx协程池大小 maxWorkers=%v maxCapacity=%v", maxWorkers, maxCapacity)
	})
	return this
}

func (this *Server) WithService(service IService) *Server {
	this.beforeOptionals = append(this.beforeOptionals, func(server *Server) {
		this.service = append(this.service, service)
		log.InfoTag("init", "装载逻辑服务[%v@%v]", service.GetName(), service.GetVersion())
	})
	return this
}

func (this *Server) WithRandomServicePort(minPort, maxPort int32) *Server {
	var ()
	this.minPort = minPort
	this.maxPort = maxPort
	return this
}

func (this *Server) WithCache(module tgf.CacheModule) {
	var ()
	switch module {
	case tgf.CacheModuleRedis:
		db.WithCacheModule(module)
	}
}

// withServiceClient
//
//	@Description: 注册rpcx的客户端程序
//	@receiver this
func (this *Server) withServiceClient() *Server {
	var ()
	//
	this.afterOptionals = append(this.afterOptionals, func(server *Server) {
		newRPCClient().startup()
		log.InfoTag("init", "装载RPCClient服务")
	})
	return this
}

func (this *Server) WithGateway(port string) *Server {
	var ()
	this.beforeOptionals = append(this.beforeOptionals, func(server *Server) {
		builder := newTCPBuilder()
		builder.WithPort(port)
		gateway := GatewayService(builder)
		this.service = append(this.service, gateway)
		log.InfoTag("init", "装载逻辑服务[%v@%v]", gateway.GetName(), gateway.GetVersion())
	})
	return this
}

func (this *Server) Run() chan bool {
	var (
		serviceName    string
		ip             string
		_logServiceMsg string
	)
	// TODO 如果有需要，可以对Optional进行优先级的控制，控制加载顺序
	for _, beforeOptional := range this.beforeOptionals {
		beforeOptional(this)
	}
	/**启动逻辑链*/
	//注册rpcx服务
	this.rpcServer = server.NewServer(server.WithPool(this.maxWorkers, this.maxCapacity))

	port := tgf.GetStrConfig[string](tgf.EnvironmentServicePort)
	if this.minPort > 0 && this.maxPort > this.minPort {
		port = fmt.Sprintf("%v", rand.Int31n(this.maxPort-this.minPort)+this.minPort)
	}
	//rpcx加入服务发现组件
	ip = fmt.Sprintf("%v:%v", util.GetLocalHost(), port)
	discovery := internal.GetDiscovery()
	//如果加入了服务注册，那么走服务注册的流程
	if discovery != nil {
		this.rpcServer.Plugins.Add(discovery.RegisterServer(ip))
		//注册服务到服务发现上,允许多个服务，注册到一个节点
		for _, service := range this.service {
			serviceName = fmt.Sprintf("%v", service.GetName())
			metaData := fmt.Sprintf("version=%v", service.GetVersion())
			err := this.rpcServer.RegisterName(serviceName, service, metaData)
			if err != nil {
				log.Error("[init] 注册服务发现失败 serviceName=%v metaDat=%v error=%v", serviceName, metaData, err)
				continue
			}

			if startupOK, startupErr := service.Startup(); !startupOK {
				log.Error("[init] 服务启动异常 serviceName=%v error=%v", serviceName, startupErr)
				continue
			}
			_logServiceMsg += serviceName + " " + metaData + ","
			log.InfoTag("init", "注册服务发现 serviceName=%v metaDat=%v", serviceName, metaData)
		}
	}

	util.Go(func() {
		if err := this.rpcServer.Serve("tcp", ip); err != nil {
			log.Error("[init] rpcx务启动异常 serviceName=%v addr=%v err=%v", serviceName, ip, err)
			os.Exit(0)
			return
		}
	})

	//启动后执行的业务
	for _, afterOptional := range this.afterOptionals {
		afterOptional(this)
	}

	util.Go(func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		for _, service := range this.service {
			service.Destroy(service)
		}
	})

	//启用服务,使用tcp
	log.InfoTag("init", "rpcx服务启动成功 addr=%v service=[%v] ", _logServiceMsg, ip)
	return this.closeChan
}

func NewRPCServer() *Server {
	rpcServer := &Server{}
	rpcServer.afterOptionals = make([]Optional, 0)
	rpcServer.beforeOptionals = make([]Optional, 0)
	rpcServer.maxWorkers = defaultMaxWorkers
	rpcServer.maxCapacity = defaultMaxCapacity
	rpcServer.closeChan = make(chan bool, 1)
	//
	rpcServer.withConsulDiscovery()
	rpcServer.withServiceClient()
	return rpcServer
}

var rpcClient *Client

type Client struct {
	clients     *hashmap.Map[string, client.XClient]
	noReplyChan chan *client.Call
}

type ClientOptional struct {
}

func newRPCClient() *ClientOptional {
	return &ClientOptional{}
}

// startup
// @Description: 启动rpc客户端
// @receiver this
func (this *ClientOptional) startup() {
	var ()
	//
	rpcClient = new(Client)
	rpcClient.clients = hashmap.New[string, client.XClient]()
	rpcClient.noReplyChan = make(chan *client.Call, 1e5)
	util.Go(func() {
		for true {
			select {
			case <-rpcClient.noReplyChan:
				//if ok {
				//log.DebugTag("monitor", "servicePath=%v serviceMethod=%v uid %v", call.ServicePath, call.ServiceMethod, call.Metadata[tgf.ContextKeyUserId])
				//log.DebugTag("rpc", "no reply service path %v ", call.ServicePath)
				//}
			}
		}
	})
	//注册一个basePath的路径
	discovery := internal.GetDiscovery()
	baseDiscovery := discovery.RegisterDiscovery("")
	//获取当前已经注册了的服务
	for _, v := range baseDiscovery.GetServices() {
		if strings.Index(v.Key, "/") > 0 {
			rpcClient.registerClient(discovery, strings.Split(v.Key, "/")[0])
		}
	}
	rpcClient.watchBaseDiscovery(discovery, baseDiscovery)

}

func (this *Client) watchBaseDiscovery(d internal.IRPCDiscovery, discovery *client2.ConsulDiscovery) {
	var ()
	util.Go(func() {
		for {
			select {
			case kv := <-discovery.WatchService():
				for _, v := range kv {
					if strings.Index(v.Key, "/") > 0 {
						moduleName := strings.Split(v.Key, "/")[0]
						if dis := internal.GetDiscovery().GetDiscovery(moduleName); dis != nil {
							continue
						}
						log.DebugTag("discovery", "base discovery service %v,%v", v.Key, v.Value)
						this.registerClient(d, moduleName)
					}
				}
			}
		}
	})
}

func (this *Client) registerClient(d internal.IRPCDiscovery, moduleName string) (xclient client.XClient) {
	var ()
	discovery := d.RegisterDiscovery(moduleName)
	option := client.DefaultOption

	//if moduleName == tgf.GatewayServiceModuleName {
	//	option.SerializeType = protocol.SerializeNone
	//}
	xclient = client.NewXClient(moduleName, client.Failover, client.SelectByUser, discovery, option)
	//自定义路由
	xclient.SetSelector(NewCustomSelector(moduleName))
	//自定义响应handler
	xclient.GetPlugins().Add(NewRPCXClientHandler())
	this.clients.Set(moduleName, xclient)
	log.InfoTag("init", "注册rpcx client 服务 module=%v ", moduleName)
	return
}

func (this *Client) getClient(moduleName string) (xclient client.XClient) {
	if val, ok := this.clients.Get(moduleName); ok {
		xclient = val
	}
	return
}

func getRPCClient() *Client {

	if rpcClient == nil {
		singletonLock.Lock()
		defer singletonLock.Unlock()
		if rpcClient == nil {
			newRPCClient().startup()
			log.InfoTag("init", "装载RPCClient服务")
		}
		//log.Warn("[rpc] RPCClient没有初始化,清调用rpc.NewRPCClient函数进行实例化")
	}
	return rpcClient
}

type Call struct {
	rpcxCall *client.Call
}

func newCall(rpcxCall *client.Call) (call *Call) {
	call = &Call{}
	call.rpcxCall = rpcxCall
	return
}

// Done
//
//	@Description: 会阻塞
//	@receiver this
//	@return error
func (this *Call) Done() error {
	var ()
	cal := <-this.rpcxCall.Done
	return cal.Error
}

func sendMessage(ct IUserConnectData, moduleName, serviceName string, args, reply interface{}) (*Call, error) {
	var (
		//TODO 这里的chan，可以根据用户，每个用户自己维护自己的一个chan，这样可以保证，用户级别的消息队列
		rc      = getRPCClient()
		xclient = rc.getClient(moduleName)
	)
	if xclient == nil {
		return nil, errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v", moduleName))
	}
	call, err := xclient.Go(ct.GetContextData(), serviceName, args, reply, ct.GetChannel())
	return newCall(call), err
}

// SendRPCMessage [Req, Res any]
//
//	@Description: 远程rpc调用
//	@param ct
//	@param api
//	@param Res]
//	@return res
//	@return err
func SendRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) (res Res, err error) {
	var (
		done    = make(chan *client.Call, 1)
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)

	if xclient == nil {
		return res, errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v serviceName=%v", api.ModuleName, api.Name))
	}
	call, err := xclient.Go(ct, api.Name, api.args, api.reply, done)
	if err != nil {
		return res, errors.New(fmt.Sprintf("rpc请求异常 moduleName=%v serviceName=%v error=%v", api.ModuleName, api.Name, err))
	}
	//这里需要处理超时，避免channel的内存泄漏
	select {
	case <-time.After(time.Second * 5):
		call.Error = tgf.ErrorRPCTimeOut
		break
	case <-call.Done:
		break
	}

	defer func() {
		if call.Error != nil {
			log.WarnTag("tcp", "RPC module=%v serviceName=%v error=%v", api.ModuleName, api.Name, call.Error)
		}
	}()
	return api.reply, call.Error
}

// SendAsyncRPCMessage [Req, Res any]
// @Description:  异步rpc请求,使用该接口时,需要确保call中的chan被消费, 避免chan的泄露
// @param ct
// @param api
// @return *client.Call
// @return error
func SendAsyncRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) (*Call, error) {
	var (
		done    = make(chan *client.Call, 1)
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)
	if xclient == nil {
		return nil, errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v", api.ModuleName))
	}
	call, err := xclient.Go(ct, api.Name, api.args, api.reply, done)
	return newCall(call), err
}

func SendNoReplyRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) error {
	var (
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)
	if xclient == nil {
		return errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v", api.ModuleName))
	}
	_, err := xclient.Go(ct, api.Name, api.args, api.reply, rc.noReplyChan)
	return err
}

func SendToGate(ct context.Context, pbMessage proto.Message) error {
	data, err := proto.Marshal(pbMessage)
	req := &ToUserReq{
		Data:   data,
		UserId: GetUserId(ct),
	}
	if err != nil {
		return err
	}
	err = SendNoReplyRPCMessage[*ToUserReq, *ToUserRes](ct, ToUser.New(req, &ToUserRes{}))
	return err
}

func NewUserContext(userId string) context.Context {
	ct := share.NewContext(context.Background())
	initData := make(map[string]string)
	initData[tgf.ContextKeyUserId] = userId
	ct.SetValue(share.ReqMetaDataKey, initData)
	return ct
}
