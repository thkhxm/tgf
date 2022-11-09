package tframework

//***************************************************
//author tim.huang
//2022/8/10
//
//
//***************************************************

// ITServer
// @Description: 服务相关接口
type ITServer interface {
	// StartupServer
	// @Description:启动服务
	//
	StartupServer()
	// AddOptions
	// @Description: 添加各个阶段的切面行为
	// @receiver s
	// @param status
	// @param options
	// @return ITServer
	AddOptions(status TServerStatus, options func(data interface{})) ITServer

	// GetModule
	// @Description: 获取模块数据
	// @return ITModule
	//
	GetModule() ITModule
}

// ITServerPlugin
// @Description: 服务器插件相关接口
type ITServerPlugin interface {

	// InitPlugin
	// @Description: 初始化插件,只会运行一次
	//
	InitPlugin()
	// StartPlugin
	// @Description: 启动插件,每个module都会执行一次
	//
	StartPlugin()
}

type ITModule interface {
	GetModuleName() (moduleName string)
	GetVersion() (_version string)
	GetAddress() (_address string)
	GetFullAddress() (_address string)
	GetPort() (_port int)
	GetPlugin() int64
	AddPlugin(plugin TServerPlugin) int64
}

type IRPCService interface {
	Send(f interface{}, rpcType int32, args *interface{}, reply *interface{})

	RegisterRPCService(f interface{}, moduleName, version string)
}
