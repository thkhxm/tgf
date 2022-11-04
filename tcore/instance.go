package tcore

import (
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/internal/plugin"
	"tframework.com/rpc/tcore/tlog"
)

//***************************************************
//author tim.huang
//2022/11/4
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

var Config *config.TConfig
var Log tframework.ILogService

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

func init() {
	//加载log
	Log = tlog.NewTLogService(plugin.NewDefaultLogger())
	//加载配置
	Config = new(config.TConfig)
	plugin.GetConfigPlugin().GetVI().Unmarshal(Config)

}
