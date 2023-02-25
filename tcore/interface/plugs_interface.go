package tframework

import (
	"tframework.com/rpc/tcore/config"
	"time"
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

type ILogService interface {
	Info(format string, v ...interface{})

	Debug(format string, v ...interface{})

	Warning(format string, v ...interface{})

	Error(format string, v ...interface{})

	ErrorS(format string, v ...interface{})

	WarningS(format string, v ...interface{})

	InfoS(format string, v ...interface{})

	DebugS(format string, v ...interface{})
}

type IConfigService interface {
	GetAPIServices() []*config.APIConfig
	GetModules() []*config.ModuleConfig
	GetDiscovery() *config.DiscoveryConfig
	GetTCPServer() *config.TCPServerConfig
}

type IRedisService interface {
	Get(key string, instance interface{}) error
	GetString(key string) string
	GetMap(key string) map[string]string

	PutMapFiled(key, filedKey, val string, expires time.Duration)
	Set(key string, instance interface{}, expires time.Duration) error
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

func init() {
}
