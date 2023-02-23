package rpc

import (
	"fmt"
	"github.com/smallnest/rpcx/server"
	"github.com/thkhxm/tgf"
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
	discovery internal.IRPCDiscovery
	//
	optionals []Optional
	//
	maxWorkers  int
	maxCapacity int
	//
	service []IService
}

type Optional func(*Server)

func (this *Server) WithConsulDiscovery() *Server {
	var ()
	this.optionals = append(this.optionals, func(server *Server) {
		server.discovery = internal.NewConsulDiscovery()
		log.Info("[init] 装载consul discovery模块")
	})
	return this
}

func (this *Server) WithServerPool(maxWorkers, maxCapacity int) *Server {
	this.optionals = append(this.optionals, func(server *Server) {
		server.maxWorkers = maxWorkers
		server.maxCapacity = maxCapacity
		log.Info("[init] 修改rpcx协程池大小 maxWorkers=%v maxCapacity=%v", maxWorkers, maxCapacity)
	})
	return this
}

func (this *Server) WithService(service IService) *Server {
	this.optionals = append(this.optionals, func(server *Server) {
		this.service = append(this.service, service)
		log.Info("[init] 装载逻辑服务[%v@%v]", service.GetName(), service.GetVersion())
	})
	return this
}

// WithServiceClient
//
//	@Description: 注册rpcx的客户端程序
//	@receiver this
func (this *Server) WithServiceClient() {
	var ()
	//
}

// TODO 是否预留部分aop相关的切面

func (this *Server) Run() {
	var (
		serviceName    string
		ip             string
		_logServiceMsg string
	)
	// TODO 如果有需要，可以对Optional进行优先级的控制，控制加载顺序
	for _, optional := range this.optionals {
		optional(this)
	}
	/**启动逻辑链*/
	//注册rpcx服务
	this.rpcServer = server.NewServer(server.WithPool(this.maxWorkers, this.maxCapacity))

	//rpcx加入服务发现组件
	ip = fmt.Sprintf("%v:%v", util.GetLocalHost(), tgf.GetServicePort())

	//如果加入了服务注册，那么走服务注册的流程
	if this.discovery != nil {
		this.rpcServer.Plugins.Add(this.discovery.RegisterServer(ip))
		//注册服务到服务发现上,允许多个服务，注册到一个节点
		for _, service := range this.service {
			serviceName = fmt.Sprintf("%v", service.GetName())
			metaData := fmt.Sprintf("version=%v", service.GetVersion())
			this.rpcServer.RegisterName(serviceName, service, metaData)
			_logServiceMsg += serviceName + " " + metaData + ","
			log.Info("[init] 注册服务发现 serviceName=%v metaDat=%v", serviceName, metaData)
		}
	}

	//TODO 后期考虑是否支持点对点的本地连接配置
	util.Go(func() {
		if err := this.rpcServer.Serve("tcp", ip); err != nil {
			log.Error("[init] rpcx务启动异常 serviceName=%v addr=%v err=%v", serviceName, ip, err)
			os.Exit(0)
			return
		}
	})
	//启用服务,使用tcp
	log.Info("[init] rpcx服务启动成功 addr=%v service=[%v] ", _logServiceMsg, ip)
}
func (this *Server) GetServer() *server.Server {
	var ()
	return this.rpcServer
}

func NewRPCServer() *Server {
	server := &Server{}
	server.optionals = make([]Optional, 0)
	server.maxWorkers = defaultMaxWorkers
	server.maxCapacity = defaultMaxCapacity
	return server
}
