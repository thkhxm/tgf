package rpc

import (
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc/internal"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

//

type RPCServer struct {
	discovery internal.IRPCDiscovery
	optionals []Optional
}

type Optional func(*RPCServer)

func (this *RPCServer) WithConsulDiscovery() {
	var ()
	this.optionals = append(this.optionals, func(server *RPCServer) {
		server.discovery = internal.NewConsulDiscovery()
		log.Info("[init] 装载consul discovery模块")
	})
}

func (this *RPCServer) Run() {
	var ()
	//TODO 如果有需要，可以对Optional进行优先级的控制，控制加载顺序
	for _, optional := range this.optionals {
		optional(this)
	}
}

func NewRPCServer(optionals ...Optional) *RPCServer {
	server := &RPCServer{}
	server.optionals = optionals
	return server
}
