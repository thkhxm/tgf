package plugin

import "tframework.com/rpc/tcore/interface"

// ***************************************************
// author tim.huang
// 2022/8/10
//
// ***************************************************

// BasePlugin
// @Description: 基础插件模板
type BasePlugin struct {
	plugsType tframework.TServerPlugin //插件类型
}

func (receiver *BasePlugin) StartPlugin() {

}
