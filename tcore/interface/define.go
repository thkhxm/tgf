package tframework

import "tframework.com/rpc/tcore/internal/define"

//***************************************************
//author tim.huang
//2022/8/10
//
//
//***************************************************

type TServerStatus int8

const (
	// StartBefore 启动前
	StartBefore TServerStatus = iota
	// StartAfter 启动后
	StartAfter
)

// TServerPlugin 模块启动配置，最多支持64个模块的配置
type TServerPlugin int64

const (
	Log    TServerPlugin = 0 //log模块，默认启动
	Consul               = 1 << iota
	P2P
)

// CheckServerPlugs val & TServerPlugin == TServerPlugin 判断模块是否开启
func CheckServerPlugs(base int64, val TServerPlugin) (open bool) {
	open = base&int64(val) == int64(val)
	return
}

//

func GetAddress() string {
	return *define.Address
}

func GetCallDepth() int {
	return *define.CallDepth
}

func GetModules() string {
	return *define.Modules
}

func GetPort() int {
	return *define.Port
}

func GetConfigPath() string {
	return *define.ConfigPath
}
