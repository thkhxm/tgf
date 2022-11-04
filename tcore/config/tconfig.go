package config

import (
	"tframework.com/rpc/tcore/internal/define"
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

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

type TConfig struct {
	Log    *LogConfig    //log相关配置
	Server *ServerConfig //服务器相关配置
}

type ServerConfig struct {
	Modules []*ModulesConfig //模块配置
}

type ModulesConfig struct {
	ModuleName    string //模块名称
	ModuleVersion string //模块版本
	Address       string //服务器监听地址
	Port          int    //服务器监听端口
}

type LogConfig struct {
	Depth int //log调用深度
}

//***********************    struct_end    ****************************

func (this TConfig) GetCallDepth() int {
	if this.Log.Depth == 0 {
		return *define.CallDepth
	}
	return this.Log.Depth
}

func (this TConfig) GetModules() []*ModulesConfig {
	return this.Server.Modules
}

func init() {

}
