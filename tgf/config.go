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
	mapping[EnvironmentRuntimeModule] = &config{env: EnvironmentRuntimeModule, val: defaultRuntimeModule}
	mapping[EnvironmentConsulAddress] = &config{env: EnvironmentConsulAddress, val: defaultConsulAddress}
	mapping[EnvironmentConsulPath] = &config{env: EnvironmentConsulPath, val: defaultConsulPath}
	mapping[EnvironmentRedisAddr] = &config{env: EnvironmentRedisAddr, val: defaultRedisAddr}
	mapping[EnvironmentRedisPassword] = &config{env: EnvironmentRedisPassword, val: defaultRedisPassword}
	mapping[EnvironmentRedisDB] = &config{env: EnvironmentRedisDB, val: defaultRedisDB}
	mapping[EnvironmentServicePort] = &config{env: EnvironmentServicePort, val: defaultServicePort}

	//初始化配置数据
	for _, m := range mapping {
		m.initVal()
		fmt.Sprintf("env=%v val=%v", m.env, m.val)
		fmt.Println()
	}
}

// 配置默认值
const (
	defaultLogPath     = "./log/tgf.log"
	defaultLogLevel    = "debug"
	defaultIgnoredTags = ""

	defaultRuntimeModule = "dev"

	defaultConsulAddress = "127.0.0.1:8500"
	defaultConsulPath    = "/tgf"

	defaultServicePort = "8082"

	defaultRedisAddr     = "127.0.0.1:6379"
	defaultRedisPassword = ""
	defaultRedisDB       = "1"
)

func (this *config) initVal() *config {
	var (
		res = os.Getenv(string(this.env))
	)
	if res != "" {
		fmt.Sprintf("[init] 配置 env=%v 从 %v 修改为 %v", this.env, this.val, res)
		fmt.Println()
		this.val = res
	}
	return this
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
