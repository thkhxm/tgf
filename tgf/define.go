package tgf

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

const (
	// RuntimeModuleDev 开发环境
	RuntimeModuleDev = "dev"

	// RuntimeModuleTest 测试环境
	RuntimeModuleTest = "test"

	// RuntimeModuleRelease 生产环境
	RuntimeModuleRelease = "release"
)

const (
	// EnvironmentLoggerPath 日志输出路径
	EnvironmentLoggerPath = "LOG_PATH"

	// EnvironmentLoggerLevel 日志最低输出级别
	EnvironmentLoggerLevel = "LOG_LEVEL"

	// EnvironmentRuntimeModule 运行环境,有以下可选运行环境
	//
	// RuntimeModuleDev RuntimeModuleTest RuntimeModuleRelease
	EnvironmentRuntimeModule = "RuntimeModule"

	EnvironmentConsulAddress = "ConsulAddress"
	EnvironmentConsulPath    = "ConsulPath"

	EnvironmentServicePort = "ServicePort"
)
