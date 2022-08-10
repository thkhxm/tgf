package tcore

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
	AddOptions(status TServerStatus, options ...ITServerOptions) ITServer
}

// ITServerOptions
// @Description: 启动过程中相关切面，行为
type ITServerOptions interface {
}
