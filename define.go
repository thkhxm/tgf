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

	// ---- v2 新增：log 包的完整可配置项（原先是硬编码的 lumberjack 参数） ----

	// EnvironmentLoggerMaxSize 单个日志文件的最大大小（MB），默认 512。
	EnvironmentLoggerMaxSize Environment = "LogMaxSize"
	// EnvironmentLoggerMaxAge 日志文件最多保留的天数。0 表示永不按时间删除，默认 0。
	EnvironmentLoggerMaxAge Environment = "LogMaxAge"
	// EnvironmentLoggerMaxBackups 日志文件最多保留的数量。默认 100。
	EnvironmentLoggerMaxBackups Environment = "LogMaxBackups"
	// EnvironmentLoggerCompress 是否对滚动后的旧日志文件启用 gzip 压缩。默认 false。
	EnvironmentLoggerCompress Environment = "LogCompress"
	// EnvironmentLoggerLocalTime 归档文件名是否使用本地时间。默认 true。
	EnvironmentLoggerLocalTime Environment = "LogLocalTime"
	// EnvironmentLoggerTimeFormat 日志时间戳格式（Go time layout），默认 "2006-01-02 15:04:05.000"。
	EnvironmentLoggerTimeFormat Environment = "LogTimeFormat"
	// EnvironmentLoggerServiceFile service tag 专用日志文件名（相对于 LogPath 所在目录），默认 "service/service.log"。
	EnvironmentLoggerServiceFile Environment = "LogServiceFile"
	// EnvironmentLoggerDBFile db tag 专用日志文件名（相对于 LogPath 所在目录），默认 "db/db.log"。
	EnvironmentLoggerDBFile Environment = "LogDBFile"

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
	// EnvironmentRedisCluster redis cluster 开关 0 关闭 1 开启,默认关闭
	EnvironmentRedisCluster Environment = "RedisCluster"

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

	// ---- v2 新增：MySQL 连接池参数（原先是硬编码 10/200/300s） ----

	// EnvironmentMySqlMaxIdleConns 最大空闲连接数，默认 10
	EnvironmentMySqlMaxIdleConns Environment = "MySqlMaxIdleConns"
	// EnvironmentMySqlMaxOpenConns 最大打开连接数，默认 200
	EnvironmentMySqlMaxOpenConns Environment = "MySqlMaxOpenConns"
	// EnvironmentMySqlConnMaxLifetimeSec 连接最大存活时间（秒），默认 300
	EnvironmentMySqlConnMaxLifetimeSec Environment = "MySqlConnMaxLifetimeSec"

	// EnvironmentServicePort 当前进程提供的服务端口
	EnvironmentServicePort    = "ServicePort"
	EnvironmentServiceAddress = "ServiceAddress"

	EnvironmentGatePush = "GatePush"

	// ---- v2 新增：RPC / 网关层运行参数 ----

	// EnvironmentRPCDefaultTimeoutMs 默认 RPC 调用超时（毫秒），默认 5000
	EnvironmentRPCDefaultTimeoutMs Environment = "RPCDefaultTimeoutMs"
	// EnvironmentTCPDeadLineSec TCP/WS 网关连接读 idle 超时（秒），默认 60
	EnvironmentTCPDeadLineSec Environment = "TCPDeadLineSec"
	// EnvironmentTCPWriteTimeoutMs 网关连接写超时（毫秒），默认 5000
	EnvironmentTCPWriteTimeoutMs Environment = "TCPWriteTimeoutMs"
	// EnvironmentTCPSendChanTimeoutMs Send 推 writeChan 的最大等待时间（毫秒），默认 3000
	EnvironmentTCPSendChanTimeoutMs Environment = "TCPSendChanTimeoutMs"

	// ---- v2 新增：DB 默认缓存超时 ----

	// EnvironmentDBCacheTimeoutSec Redis 缓存默认 TTL（秒），默认 86400*3 (3天)
	EnvironmentDBCacheTimeoutSec Environment = "DBCacheTimeoutSec"
	// EnvironmentDBMemTimeoutSec 内存缓存默认 TTL（秒），默认 3600*3 (3小时)
	EnvironmentDBMemTimeoutSec Environment = "DBMemTimeoutSec"
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
	ContextKeyUserId           = "UserId"
	ContextKeyRPCType          = "RPCType"
	ContextKeyTemplateUserId   = "TemplateUserId"
	ContextKeyNodeId           = "NodeId"
	ContextKeyCloseLocalCache  = "CloseLocalCache"
	ContextKeyBroadcastUserIds = "BroadcastUserIds"
	ContextKeyTRACEID          = "TraceId"
	ContextKeyHash             = "__hash"
)

const (
	RPCTip          = "rpc_tip"
	RPCBroadcastTip = "rpc_broadcast_tip"
)

const GatewayServiceModuleName = "gate"
const MonitorServiceModuleName = "monitor"
const AdminServiceModuleName = "admin"

var NodeId = ""
var ServerModule = false
