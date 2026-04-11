package internal

import (
	"sync"

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

// discovery 是 package 级 singleton，由 discoveryOnce 保护并发初始化。
// A6 修复：原实现用 `if discovery != nil { return }` 做 nil check，两个 goroutine
// 可以同时通过检查然后各自 new 一个 ConsulDiscovery，丢失一个并留下不一致的
// discoveryMap。sync.Once 保证整个初始化路径只跑一次。
var (
	discovery     IRPCDiscovery
	discoveryOnce sync.Once
)

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

// UseConsulDiscovery 幂等地初始化全局 discovery 单例为 ConsulDiscovery。
// A4 之后这个函数被 buildPreServeHooks 里"默认 Consul Hook"调用；也可能被
// 业务代码显式调用——两种路径并存时 sync.Once 保证只 new 一次。
func UseConsulDiscovery() {
	discoveryOnce.Do(func() {
		cd := new(ConsulDiscovery)
		cd.initStruct()
		discovery = cd
		log.InfoTag("init", "装载consul discovery模块")
	})
}

// GetDiscovery 返回当前 discovery 单例的快照。
//
// A6 行为变更：**不再自动触发 UseConsulDiscovery**。
// 原实现里 `if discovery == nil { UseConsulDiscovery() }` 的"懒加载"和 A4 的
// `WithoutConsul()` 直接冲突——WithoutConsul 后 Run 里调 GetDiscovery 会把
// Consul 装回来，让 disableConsul 失效。现在 GetDiscovery 只读不写：
//   - 调用方（rpcserver.Run 等）自己判断 nil 走分支
//   - Consul 的装载只能通过显式 UseConsulDiscovery 或 buildPreServeHooks 的 Hook
//
// 调用方已经在 rpcserver.Run 里用 `if discovery != nil` guard 过了，所以这次
// 行为变更不会破坏现有路径，但会让 `WithoutConsul()` 真的生效。
func GetDiscovery() IRPCDiscovery {
	return discovery
}

// ResetDiscoveryForTest 仅用于单测：清空 discovery 单例和 sync.Once，
// 让下一次 UseConsulDiscovery 能重新初始化。生产代码不要调用。
func ResetDiscoveryForTest() {
	discovery = nil
	discoveryOnce = sync.Once{}
}
