package plugin

import "tframework.com/rpc/tcore/interface"

// ***************************************************
// author tim.huang
// 2022/8/10
//
// ***************************************************
var PluginsFactory = Plugins{p: make(map[tframework.TServerPlugin]tframework.ITServerPlugin)}

// BasePlugin
// @Description: 基础插件模板
type BasePlugin struct {
	plugsType tframework.TServerPlugin //插件类型
}

func (this *BasePlugin) GetPluginType() tframework.TServerPlugin {
	return this.plugsType
}

type Plugins struct {
	p map[tframework.TServerPlugin]tframework.ITServerPlugin
}

func (this *Plugins) AddPlugin(plugin tframework.TServerPlugin, serverPlugin tframework.ITServerPlugin) {
	this.p[plugin] = serverPlugin
}
