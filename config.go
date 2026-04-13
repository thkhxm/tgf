package tgf

import (
	"flag"
	"fmt"
	"github.com/joho/godotenv"
	"github.com/thkhxm/tgf/util"

	"os"
	"strings"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

type config struct {
	env Environment
	val string
}

var mapping = make(map[Environment]*config)

func initMapping() {
	mapping[EnvironmentLoggerPath] = &config{env: EnvironmentLoggerPath, val: defaultLogPath}
	mapping[EnvironmentLoggerLevel] = &config{env: EnvironmentLoggerLevel, val: defaultLogLevel}
	mapping[EnvironmentLoggerIgnoredTags] = &config{env: EnvironmentLoggerIgnoredTags, val: defaultIgnoredTags}
	// v2 新增 log 配置项
	mapping[EnvironmentLoggerMaxSize] = &config{env: EnvironmentLoggerMaxSize, val: defaultLogMaxSize}
	mapping[EnvironmentLoggerMaxAge] = &config{env: EnvironmentLoggerMaxAge, val: defaultLogMaxAge}
	mapping[EnvironmentLoggerMaxBackups] = &config{env: EnvironmentLoggerMaxBackups, val: defaultLogMaxBackups}
	mapping[EnvironmentLoggerCompress] = &config{env: EnvironmentLoggerCompress, val: defaultLogCompress}
	mapping[EnvironmentLoggerLocalTime] = &config{env: EnvironmentLoggerLocalTime, val: defaultLogLocalTime}
	mapping[EnvironmentLoggerTimeFormat] = &config{env: EnvironmentLoggerTimeFormat, val: defaultLogTimeFormat}
	mapping[EnvironmentLoggerServiceFile] = &config{env: EnvironmentLoggerServiceFile, val: defaultLogServiceFile}
	mapping[EnvironmentLoggerDBFile] = &config{env: EnvironmentLoggerDBFile, val: defaultLogDBFile}
	// v2 新增：MySQL 连接池
	mapping[EnvironmentMySqlMaxIdleConns] = &config{env: EnvironmentMySqlMaxIdleConns, val: defaultMySqlMaxIdleConns}
	mapping[EnvironmentMySqlMaxOpenConns] = &config{env: EnvironmentMySqlMaxOpenConns, val: defaultMySqlMaxOpenConns}
	mapping[EnvironmentMySqlConnMaxLifetimeSec] = &config{env: EnvironmentMySqlConnMaxLifetimeSec, val: defaultMySqlConnMaxLifetimeSec}
	// v2 新增：RPC / 网关运行参数
	mapping[EnvironmentRPCDefaultTimeoutMs] = &config{env: EnvironmentRPCDefaultTimeoutMs, val: defaultRPCDefaultTimeoutMs}
	mapping[EnvironmentTCPDeadLineSec] = &config{env: EnvironmentTCPDeadLineSec, val: defaultTCPDeadLineSec}
	mapping[EnvironmentTCPWriteTimeoutMs] = &config{env: EnvironmentTCPWriteTimeoutMs, val: defaultTCPWriteTimeoutMs}
	mapping[EnvironmentTCPSendChanTimeoutMs] = &config{env: EnvironmentTCPSendChanTimeoutMs, val: defaultTCPSendChanTimeoutMs}
	// v2 新增：DB 默认缓存超时
	mapping[EnvironmentDBCacheTimeoutSec] = &config{env: EnvironmentDBCacheTimeoutSec, val: defaultDBCacheTimeoutSec}
	mapping[EnvironmentDBMemTimeoutSec] = &config{env: EnvironmentDBMemTimeoutSec, val: defaultDBMemTimeoutSec}
	mapping[EnvironmentRuntimeModule] = &config{env: EnvironmentRuntimeModule, val: defaultRuntimeModule}
	mapping[EnvironmentConsulAddress] = &config{env: EnvironmentConsulAddress, val: defaultConsulAddress}
	mapping[EnvironmentConsulPath] = &config{env: EnvironmentConsulPath, val: defaultConsulPath}
	mapping[EnvironmentRedisAddr] = &config{env: EnvironmentRedisAddr, val: defaultRedisAddr}
	mapping[EnvironmentRedisPassword] = &config{env: EnvironmentRedisPassword, val: defaultRedisPassword}
	mapping[EnvironmentRedisDB] = &config{env: EnvironmentRedisDB, val: defaultRedisDB}
	mapping[EnvironmentRedisCluster] = &config{env: EnvironmentRedisCluster, val: defaultRedisCluster}
	mapping[EnvironmentServicePort] = &config{env: EnvironmentServicePort, val: defaultServicePort}
	mapping[EnvironmentServiceAddress] = &config{env: EnvironmentServiceAddress, val: defaultServiceAddress}

	mapping[EnvironmentMySqlUser] = &config{env: EnvironmentMySqlUser, val: defaultMySqlUser}
	mapping[EnvironmentMySqlPwd] = &config{env: EnvironmentMySqlPwd, val: defaultMySqlPwd}
	mapping[EnvironmentMySqlAddr] = &config{env: EnvironmentMySqlAddr, val: defaultMySqlAddr}
	mapping[EnvironmentMySqlPort] = &config{env: EnvironmentMySqlPort, val: defaultMySqlPort}
	mapping[EnvironmentMySqlDB] = &config{env: EnvironmentMySqlDB, val: defaultMySqlDB}
	mapping[EnvironmentGatePush] = &config{env: EnvironmentGatePush, val: defaultGatePush}

	//初始化配置数据
	for _, m := range mapping {
		m.initVal()
		fmt.Printf("env=%v val=%v\n", m.env, m.val)
	}
}

// 配置默认值
const (
	defaultMySqlUser = "root"
	defaultMySqlPwd  = "123456"
	defaultMySqlAddr = "127.0.0.1"
	defaultMySqlPort = "3306"
	defaultMySqlDB   = "tgf"

	defaultLogPath     = "./log/tgf.log"
	defaultLogLevel    = "debug"
	defaultIgnoredTags = ""
	// v2 新增：lumberjack 滚动切割参数
	defaultLogMaxSize     = "512" // 单个文件最大 MB
	defaultLogMaxAge      = "0"   // 最多保留天数，0 = 不按时间删除
	defaultLogMaxBackups  = "100" // 最多保留文件数
	defaultLogCompress    = "0"   // 是否启用 gzip 压缩滚动后的文件（0=false, 1=true）
	defaultLogLocalTime   = "1"   // 归档文件名使用本地时间（0=UTC, 1=本地）
	defaultLogTimeFormat  = "2006-01-02 15:04:05.000"
	defaultLogServiceFile = "service/service.log"
	defaultLogDBFile      = "db/db.log"

	// v2 新增：MySQL 连接池
	defaultMySqlMaxIdleConns       = "10"
	defaultMySqlMaxOpenConns       = "200"
	defaultMySqlConnMaxLifetimeSec = "300"

	// v2 新增：RPC / 网关运行参数
	defaultRPCDefaultTimeoutMs   = "5000" // 5 秒
	defaultTCPDeadLineSec        = "60"
	defaultTCPWriteTimeoutMs     = "5000" // 5 秒
	defaultTCPSendChanTimeoutMs  = "3000" // 3 秒

	// v2 新增：DB 默认缓存超时
	defaultDBCacheTimeoutSec = "259200" // 3 天
	defaultDBMemTimeoutSec   = "10800"  // 3 小时

	defaultRuntimeModule = "dev"

	defaultConsulAddress = "127.0.0.1:8500"
	defaultConsulPath    = "/tgf"

	defaultServicePort    = "8082"
	defaultServiceAddress = "127.0.0.1"

	defaultRedisAddr     = "127.0.0.1:6379"
	defaultRedisPassword = ""
	defaultRedisDB       = "1"
	defaultGatePush      = "1"
	defaultRedisCluster  = "0"
)

func (c *config) initVal() *config {
	var (
		res = os.Getenv(string(c.env))
	)
	if res != "" {
		fmt.Printf("[init] 配置 env=%v 从 %v 修改为 %v\n", c.env, c.val, res)
		c.val = res
	}
	return c
}

// 配置缓存

func GetStrConfig[T int | int32 | string | int64 | float32 | float64](env Environment) (res T) {
	res, _ = util.StrToAny[T](mapping[env].val)
	return
}

func GetStrListConfig(env Environment) (res []string) {
	res = make([]string, 0)
	for _, s := range strings.Split(mapping[env].val, ",") {
		res = append(res, s)
	}
	return
}

func InitConfig() {
	//初始化配置的环境变量
	env := os.Getenv("TGFMODULE")
	if env == "" {
		env = *flag.String("TGFMODULE", RuntimeModuleDev, "RuntimeModule")
		if env == "" {
			env = defaultRuntimeModule
		}
	}
	fmt.Printf("[tgf/init.go] 当前运行模式 [TGFMODULE] 为 %v", env)
	fmt.Println()
	fileName := ".env." + env
	// 加载环境配置文件
	err := godotenv.Load(fileName)
	if err != nil {
		fmt.Printf("[init] [tgf/init.go] 找不到指定的env文件 %v", fileName)
		fmt.Println()
	}
	initMapping()
}
