package tserver

import (
	"fmt"
	"github.com/smallnest/rpcx/log"
	"github.com/smallnest/rpcx/server"
	"reflect"
	"strings"
	"tframework.com/rpc/tcore/interface"
	"tframework.com/server/common"
)

//***************************************************
//author tim.huang
//2022/8/10
//
//
//***************************************************

var rpcPrefix = "RPC"

// TServer
// @Description:
type TServer[T tframework.ITModule] struct {
	startDetails map[tframework.TServerStatus]StartDetail
	rpcServer    *server.Server

	module T
}

// StartDetail
// @Description: 启动详情
// TODO: 名字需要修改一下，跟他的业务不太符合
type StartDetail struct {
	status  tframework.TServerStatus
	options []tframework.ITServerOptions
}

func (s *TServer[T]) AddOptions(status tframework.TServerStatus, options ...tframework.ITServerOptions) tframework.ITServer {
	return s
}

func (s *TServer[T]) StartupServer() {
	s.rpcServer = server.NewServer()
	s.autoRegisterRPCService()
	s.startupDiscovery()
	s.startupServer()
}

func (s *TServer[T]) SetModule(module T) {
	s.module = module //
}

// autoRegisterRPCService
// @Description: 自动注册rpc接口
// @receiver s
func (s *TServer[T]) autoRegisterRPCService() {
	types := reflect.TypeOf(s.module)
	for i := 0; i < types.NumMethod(); i++ {
		method := types.Method(i)
		if strings.HasPrefix(method.Name, rpcPrefix) {
			//s.rpcServer.Register()
			log.Infof("find the rpc method ,method name is %v", method.Name)
		}
	}
}

// startupServer
// @Description:
// @receiver s
func (s *TServer[T]) startupServer() {
	addr := fmt.Sprintf("%v:%v", common.GetAddress(), common.GetPort())
	s.rpcServer.Serve("tcp", addr)
}

// startupDiscovery
// @Description: 启动服务发现相关插件
// @receiver s
func (s *TServer[T]) startupDiscovery() {
	//是否开启点对点服务，不使用服务发现机制
	switch {
	case tframework.CheckServerPlugs(s.module.GetPlugin(), tframework.P2P):

	case tframework.CheckServerPlugs(s.module.GetPlugin(), tframework.Consul):

	}

}

//func init() {
//	//初始化入参
//	flag.Parse()
//}
