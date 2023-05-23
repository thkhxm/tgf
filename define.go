package tgf

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

const Logo = `


__/\\\\\\\\\\\\\\\_        _____/\\\\\\\\\\\\_        __/\\\\\\\\\\\\\\\_        
 _\///////\\\/////__        ___/\\\//////////__        _\/\\\///////////__       
  _______\/\\\_______        __/\\\_____________        _\/\\\_____________      
   _______\/\\\_______        _\/\\\____/\\\\\\\_        _\/\\\\\\\\\\\_____
    _______\/\\\_______        _\/\\\___\/////\\\_        _\/\\\///////______    
     _______\/\\\_______        _\/\\\_______\/\\\_        _\/\\\_____________   
      _______\/\\\_______        _\/\\\_______\/\\\_        _\/\\\_____________  
       _______\/\\\_______        _\//\\\\\\\\\\\\/__        _\/\\\_____________ 
        _______\///________        __\////////////____        _\///______________


`

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
	EnvironmentLoggerPath Environment = "LogPath"

	// EnvironmentLoggerLevel 日志最低输出级别
	EnvironmentLoggerLevel       Environment = "LogLevel"
	EnvironmentLoggerIgnoredTags Environment = "LogIgnoredTags"

	// EnvironmentRuntimeModule 运行环境,有以下可选运行环境
	// dev test release
	// RuntimeModuleDev RuntimeModuleTest RuntimeModuleRelease
	EnvironmentRuntimeModule Environment = "RuntimeModule"

	// EnvironmentConsulAddress consul地址
	EnvironmentConsulAddress Environment = "ConsulAddress"

	// EnvironmentConsulPath consul路径
	//
	// 默认使用/tgf,如需区分不同环境可以使用自定义的不同的路径 例如 /test 或者 /dev /tim
	EnvironmentConsulPath Environment = "ConsulPath"

	// EnvironmentRedisAddr redis地址 127.0.0.1::6379
	EnvironmentRedisAddr Environment = "RedisAddr"

	// EnvironmentRedisPassword redis密码
	EnvironmentRedisPassword Environment = "RedisPassword"

	// EnvironmentRedisDB redis的db
	EnvironmentRedisDB Environment = "RedisDB"

	// EnvironmentMySqlUser mysql用户名
	EnvironmentMySqlUser Environment = "MySqlUser"

	// EnvironmentMySqlPwd mysql密码
	EnvironmentMySqlPwd Environment = "MySqlPwd"

	// EnvironmentMySqlAddr mysql地址
	EnvironmentMySqlAddr Environment = "MySqlAddr"

	// EnvironmentMySqlPort mysql端口
	EnvironmentMySqlPort Environment = "MySqlPort"

	// EnvironmentMySqlDB mysql库
	EnvironmentMySqlDB Environment = "MySqlDB"

	// EnvironmentServicePort 当前进程提供的服务端口
	EnvironmentServicePort = "ServicePort"
)

type CacheModule int

const (
	CacheModuleRedis CacheModule = iota
	CacheModuleClose
)

// redisKey
const (
	RedisKeyUserNodeMeta = "user:node:meta:%v"
)

const (
	ContextKeyUserId         = "UserId"
	ContextKeyRPCType        = "RPCType"
	ContextKeyTemplateUserId = "TemplateUserId"
	ContextKeyNodeId         = "NodeId"
)

const (
	RPCTip = "rpc_tip"
)

const GatewayServiceModuleName = "Gate"

var NodeId = ""
