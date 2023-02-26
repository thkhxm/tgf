package rpc

import (
	"context"
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/protocol"
	"github.com/smallnest/rpcx/server"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc/internal"
	"github.com/thkhxm/tgf/util"
	"os"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

const (
	defaultMaxWorkers  = 1e4
	defaultMaxCapacity = 1e4
)

// Server
// @Description:
type Server struct {
	rpcServer *server.Server

	//启动后执行的操作
	AfterOptionals []Optional
	//启动前执行
	BeforeOptionals []Optional
	//
	maxWorkers  int
	maxCapacity int
	//
	service []IService
}

type Optional func(*Server)

func (this *Server) WithConsulDiscovery() *Server {
	var ()
	this.BeforeOptionals = append(this.BeforeOptionals, func(server *Server) {
		internal.UseConsulDiscovery()
		log.Info("[init] 装载consul discovery模块")
	})
	return this
}

func (this *Server) WithServerPool(maxWorkers, maxCapacity int) *Server {
	this.BeforeOptionals = append(this.BeforeOptionals, func(server *Server) {
		server.maxWorkers = maxWorkers
		server.maxCapacity = maxCapacity
		log.Info("[init] 修改rpcx协程池大小 maxWorkers=%v maxCapacity=%v", maxWorkers, maxCapacity)
	})
	return this
}

func (this *Server) WithService(service IService) *Server {
	this.BeforeOptionals = append(this.BeforeOptionals, func(server *Server) {
		this.service = append(this.service, service)
		log.Info("[init] 装载逻辑服务[%v@%v]", service.GetName(), service.GetVersion())
	})
	return this
}

func (this *Server) WithCache(module tgf.CacheModule) {
	var ()
	switch module {
	case tgf.CacheModuleRedis:
		db.WithCacheModule(module)
	}
}

func (this *Server) WithTCPServer(port string) *Server {
	this.AfterOptionals = append(this.AfterOptionals, func(server *Server) {
		tcp := NewDefaultTCPServer()
		util.Go(func() {
			tcp.WithPort(port).Run()
		})
		log.Info("[init] 装载TCP服务")
	})
	return this
}

// WithServiceClient
//
//	@Description: 注册rpcx的客户端程序
//	@receiver this
func (this *Server) WithServiceClient() *Server {
	var ()
	//
	this.AfterOptionals = append(this.AfterOptionals, func(server *Server) {
		NewRPCClient().Startup()
		log.Info("[init] 装载RPCClient服务")
	})
	return this
}

func (this *Server) Run() {
	var (
		serviceName    string
		ip             string
		_logServiceMsg string
	)
	// TODO 如果有需要，可以对Optional进行优先级的控制，控制加载顺序
	for _, beforeOptional := range this.BeforeOptionals {
		beforeOptional(this)
	}
	/**启动逻辑链*/
	//注册rpcx服务
	this.rpcServer = server.NewServer(server.WithPool(this.maxWorkers, this.maxCapacity))

	//rpcx加入服务发现组件
	ip = fmt.Sprintf("%v:%v", util.GetLocalHost(), tgf.GetStrConfig[string](tgf.EnvironmentServicePort))
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
			_logServiceMsg += serviceName + " " + metaData + ","
			log.Info("[init] 注册服务发现 serviceName=%v metaDat=%v", serviceName, metaData)
		}
	}

	//自定义plugin
	util.Go(func() {
		if err := this.rpcServer.Serve("tcp", ip); err != nil {
			log.Error("[init] rpcx务启动异常 serviceName=%v addr=%v err=%v", serviceName, ip, err)
			os.Exit(0)
			return
		}
	})

	//启动后执行的业务
	for _, afterOptional := range this.AfterOptionals {
		afterOptional(this)
	}

	//启用服务,使用tcp
	log.Info("[init] rpcx服务启动成功 addr=%v service=[%v] ", _logServiceMsg, ip)
}

func NewRPCServer() *Server {
	rpcServer := &Server{}
	rpcServer.AfterOptionals = make([]Optional, 0)
	rpcServer.BeforeOptionals = make([]Optional, 0)
	rpcServer.maxWorkers = defaultMaxWorkers
	rpcServer.maxCapacity = defaultMaxCapacity
	return rpcServer
}

var rpcClient *Client

type Client struct {
	clients *hashmap.Map[string, client.XClient]
}

type ClientOptional struct {
}

func NewRPCClient() *ClientOptional {
	return &ClientOptional{}
}

// Startup
// @Description: 启动rpc客户端
// @receiver this
func (this *ClientOptional) Startup() {
	var ()
	//
	rpcClient = new(Client)
	rpcClient.clients = hashmap.New[string, client.XClient]()
}

func getRPCClient() *Client {
	if rpcClient == nil {
		log.Warn("[rpc] RPCClient没有初始化,清调用rpc.NewRPCClient函数进行实例化")
	}
	return rpcClient
}

func SendRPCMessage(moduleName, serviceName string, args, reply interface{}) (*client.Call, error) {
	var (
		done    = make(chan *client.Call, 10)
		rc      = getRPCClient()
		xclient = rc.getClient(moduleName)
	)
	return xclient.Go(context.Background(), serviceName, args, reply, done)
}

func (this *Client) getClient(moduleName string) (xclient client.XClient) {
	if val, ok := this.clients.Get(moduleName); ok {
		xclient = val
	} else {
		discovery := internal.GetDiscovery().GetDiscovery(moduleName)
		option := client.DefaultOption

		if moduleName == tgf.GatewayServiceModuleName {
			option.SerializeType = protocol.SerializeNone
		}

		xclient = client.NewXClient(moduleName, client.Failover, client.ConsistentHash, discovery, option)

		//自定义路由
		xclient.GetPlugins().Add(internal.NewCustomSelector())
		//自定义响应handler
		xclient.GetPlugins().Add(internal.NewRPCXClientHandler())
		this.clients.Set(moduleName, xclient)
	}
	return
}
