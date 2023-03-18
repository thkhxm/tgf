package internal

import (
	"github.com/rpcxio/rpcx-consul/client"
	"github.com/smallnest/rpcx/server"
	"github.com/thkhxm/tgf/log"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

var discovery IRPCDiscovery

// IRPCDiscovery
// @Description: rpc服务注册接口
type IRPCDiscovery interface {
	// RegisterServer
	//  @Description: 注册rpcx的服务发现
	//  @param ip 传入注册的本机ip和端口 example: 192.168.1.10:8881
	//  @return server.Plugin 返回的是rpcx所需的插件类型
	RegisterServer(ip string) server.Plugin

	RegisterDiscovery(moduleName string) *client.ConsulDiscovery
	GetDiscovery(moduleName string) *client.ConsulDiscovery
}

func UseConsulDiscovery() {
	if discovery != nil {
		return
	}
	cd := new(ConsulDiscovery)
	cd.initStruct()
	discovery = cd
	log.InfoTag("init", "装载consul discovery模块")
}

func GetDiscovery() IRPCDiscovery {
	if discovery == nil {
		UseConsulDiscovery()
	}
	return discovery
}
