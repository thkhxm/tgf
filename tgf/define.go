package tgf

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

// 运行环境
const (
	// RuntimeModuleDev 开发环境
	RuntimeModuleDev = "dev"

	// RuntimeModuleTest 测试环境
	RuntimeModuleTest = "test"

	// RuntimeModuleRelease 生产环境
	RuntimeModuleRelease = "release"
)

type Environment string

// 环境变量
const (
	// EnvironmentLoggerPath 日志输出路径
	EnvironmentLoggerPath Environment = "LOG_PATH"

	// EnvironmentLoggerLevel 日志最低输出级别
	EnvironmentLoggerLevel Environment = "LOG_LEVEL"

	// EnvironmentRuntimeModule 运行环境,有以下可选运行环境
	//
	// RuntimeModuleDev RuntimeModuleTest RuntimeModuleRelease
	EnvironmentRuntimeModule Environment = "RuntimeModule"

	EnvironmentConsulAddress Environment = "ConsulAddress"
	EnvironmentConsulPath    Environment = "ConsulPath"

	EnvironmentRedisAddr     Environment = "RedisAddr"
	EnvironmentRedisPassword Environment = "RedisPassword"
	EnvironmentRedisDB       Environment = "RedisDB"

	EnvironmentServicePort = "ServicePort"
)

type CacheModule int

const (
	CacheModuleRedis CacheModule = iota
	CacheModuleMysql
	CacheModuleMongodb
)

// redisKey
const (
	RedisKeyUserNodeMeta   = "user:node:meta:%v"
	RedisKeyUserLoginToken = "user:login:token:Mapping:%v"
)

const (
	ContextKeyUserId = "UserId"
)

var GatewayServiceModuleName = "Gate"
