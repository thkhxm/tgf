package tgf

import (
	"flag"
	"fmt"
	"github.com/joho/godotenv"
	"os"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

// 配置默认值
const (
	defaultLogPath       = "./log/tgf.log"
	defaultLogLevel      = "debug"
	defaultRuntimeModule = "dev"
)

// 配置缓存
var (
	logPath  = defaultLogPath
	logLevel = defaultLogLevel
	module   = RuntimeModuleDev
)

// GetRuntimeModule
// @Description:
// @return res
func GetRuntimeModule() (res string) {
	res = os.Getenv(EnvironmentRuntimeModule)
	if res == "" {
		res = module
	}
	return
}

func GetLogPath() (res string) {
	res = os.Getenv(EnvironmentLoggerPath)
	if res == "" {
		res = logPath
	}
	return
}

func GetLogLevel() (res string) {
	res = os.Getenv(EnvironmentLoggerLevel)
	if res == "" {
		res = logLevel
	}
	return
}

func InitConfig() {
	//初始化配置的环境变量
	env := os.Getenv("TGFMODULE")
	if env == "" {
		env = *flag.String("TGFMODULE", RuntimeModuleDev, "RuntimeModule")
		if env == "" {
			env = "dev"
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
}
