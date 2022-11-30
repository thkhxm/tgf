package tcore

import (
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/internal/plugin"
	"tframework.com/rpc/tcore/tlog"
	"tframework.com/rpc/tcore/trpcservice"
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

var Config tframework.IConfigService
var Log tframework.ILogService
var RPCService tframework.IRPCService
var Redis tframework.IRedisService

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
	//加载rpc服务
	RPCService = trpcservice.NewRPCService(Config.GetAPIServices())
}
