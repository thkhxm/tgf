package tgf

import (
	"flag"
	"fmt"
	"github.com/joho/godotenv"
	"os"
	"strings"
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
	defaultLogPath  = "./log/tgf.log"
	defaultLogLevel = "debug"

	defaultRuntimeModule = "dev"

	defaultConsulAddress = "127.0.0.1:8500"
	defaultConsulPath    = "/tgf"

	defaultServicePort = "8082"
)

// 配置缓存

var ()

func GetConsulPath() (res string) {
	res = os.Getenv(EnvironmentConsulPath)
	if res == "" {
		res = defaultConsulPath
	}
	return
}

func GetConsulAddress() []string {
	vals := os.Getenv(EnvironmentConsulAddress)
	if vals == "" {
		return []string{defaultConsulAddress}
	}
	res := make([]string, 0)
	for _, s := range strings.Split(vals, ",") {
		res = append(res, s)
	}
	return res
}

func GetServicePort() (res string) {
	res = os.Getenv(EnvironmentServicePort)
	if res == "" {
		res = defaultServicePort
	}
	return
}

func GetRuntimeModule() (res string) {
	res = os.Getenv(EnvironmentRuntimeModule)
	if res == "" {
		res = RuntimeModuleDev
	}
	return
}

func GetLogPath() (res string) {
	res = os.Getenv(EnvironmentLoggerPath)
	if res == "" {
		res = defaultLogPath
	}
	return
}

func GetLogLevel() (res string) {
	res = os.Getenv(EnvironmentLoggerLevel)
	if res == "" {
		res = defaultLogLevel
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
}
