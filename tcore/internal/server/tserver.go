package tserver

import (
	"fmt"
	"github.com/smallnest/rpcx/server"
	"sync"
	_interface "tframework.com/rpc/tcore/internal/interface"

	"tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/internal/plugin"
)

//***************************************************
//author tim.huang
//2022/8/10
//
//
//***************************************************

var rpcPrefix = "RPC"
var servicePrefix = "Service"

// TServer
// @Description:
type TServer[T tframework.ITModule] struct {
	startDetails  map[tframework.TServerStatus]*StartDetail
	rpcServer     *server.Server
	configService _interface.IServerConfigService
	module        T
}

// StartDetail
// @Description: 启动详情
// TODO: 名字需要修改一下，跟他的业务不太符合
type StartDetail struct {
	status  tframework.TServerStatus
	options []func(data interface{})
	data    interface{}
}

func (s *TServer[T]) AddOptions(status tframework.TServerStatus, options func(data interface{}), data interface{}) tframework.ITServer {
	app := s.startDetails[status]
	if app == nil {
		app = &StartDetail{
			status:  status,
			options: make([]func(data interface{}), 0),
			data:    data,
		}
		s.startDetails[status] = app
	}
	app.options = append(app.options, options)
	return s
}

func (s *TServer[T]) StartupServer() {
	s.rpcServer = server.NewServer()
	s.startupDiscovery()
	s.autoRegisterRPCService()
	s.startupServer(s.module.GetAddress(), s.module.GetPort())
}

func (this *TServer[T]) GetModule() tframework.ITModule {
	return this.module
}
func (s *TServer[T]) SetModule(module T) {
	s.module = module //
}

func (s *TServer[T]) SetConfigService(service _interface.IServerConfigService) {
	s.configService = service
}

func (s *TServer[T]) InitStruct() {
	s.startDetails = make(map[tframework.TServerStatus]*StartDetail)
}

// autoRegisterRPCService
// @Description: 自动注册rpc接口
// @receiver s
func (s *TServer[T]) autoRegisterRPCService() {
	path := fmt.Sprintf("%v %v", s.module.GetModuleName(), s.module.GetVersion())
	s.rpcServer.RegisterName(path, s.module, "")
	//if s.configService.IsGateway() {
	s.rpcServer.RegisterName(s.module.GetModuleName()+" Service", s.module, "")
	//}
}

// startupServer
// @Description:
// @receiver s
func (s *TServer[T]) startupServer(address string, port int) {
	addr := fmt.Sprintf("%v:%v", address, port)
	plugin.InfoS("服务启动成功,绑定服务地址[%v]", addr)
	start := s.startDetails[tframework.StartAfter]
	if start != nil && len(start.options) > 0 {
		for _, option := range start.options {
			option(start.data)
		}
	}
	if err := s.rpcServer.Serve("tcp", addr); err != nil {
		plugin.InfoS("启动异常 [%v] ", err)
	}
}

// startupDiscovery
// @Description: 启动服务发现相关插件
// @receiver s
func (s *TServer[T]) startupDiscovery() {
	//是否开启点对点服务，不使用服务发现机制
	switch {
	case tframework.CheckServerPlugs(s.module.GetPlugin(), tframework.P2P):

	case tframework.CheckServerPlugs(s.module.GetPlugin(), tframework.Consul):
		instanceDefaultConsulDiscovery(s.configService)
		s.addRegistryPlugin()
	}

}

func (s *TServer[T]) startupPlugins() {
	//是否开启点对点服务，不使用服务发现机制
	if tframework.CheckServerPlugs(s.module.GetPlugin(), tframework.Redis) {

	}

}

func (s *TServer[T]) addRegistryPlugin() {
	r := ConsulDiscovery.RegisterServer(s.module.GetFullAddress(), s.module.GetModuleName(), s.module)
	s.rpcServer.Plugins.Add(r)
	plugin.InfoS("[Consul] 服务 [%v] 注册到 [%v] ", s.module.GetModuleName(), s.configService.GetConsulAddressSlice())
}

var RequestMapping *sync.Map

func init() {
	RequestMapping = new(sync.Map)
}
