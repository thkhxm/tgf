package plugin

import (
	"fmt"
	"github.com/spf13/viper"
	"os"
	"reflect"
	"strings"
	"tframework.com/rpc/tcore/internal/define"
)

//***************************************************
//author tim.huang
//2022/8/24
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

var cp *ConfigPlugin

//***********************    var_end    ****************************

//***********************    interface    ****************************

func (c *ConfigPlugin) GetVI() *viper.Viper {
	return c.vi
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

type ConfigPlugin struct {
	BasePlugin
	vi *viper.Viper
}

func getData[T any](point T) {
	var key string
	var val interface{}
	tp := reflect.TypeOf(point).Elem()
	//va := reflect.ValueOf(point).Elem()
	size := tp.NumField()
	for i := 0; i < size; i++ {
		field := tp.Field(i)
		if cnf, ok := field.Tag.Lookup("cnf"); ok {
			key = cnf
		} else {
			key = fmt.Sprintf("%v.%v", tp.Name(), field.Name)
		}
		key = strings.ToLower(key)
		val = cp.GetVI().Get(key)
		InfoS("读取到配置文件路径[%v]，值为[%v]", key, val)
	}
	//cp.GetVI().Unmarshal(point)
}

//***********************    struct_end    ****************************

func init() {
	//	读取默认环境配置文件,判断当前运行环境和相关基础配置或者通用配置
	baseVI := createViper("app")
	active := baseVI.GetString("server.profiles.active")
	if os.Getenv("active") != "" {
		active = os.Getenv("active")
	}
	newDefaultConfig(fmt.Sprintf("app-%v", active))

}

func createViper(configName string) (pathConf *viper.Viper) {
	pathConf = viper.New()
	pathConf.AddConfigPath(*define.ConfigPath)
	pathConf.SetConfigName(configName)
	pathConf.SetConfigType("yaml")

	if err := pathConf.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			fmt.Println("找不到配置文件..")
		} else {
			fmt.Println("配置文件出错..")
		}
	}
	return
}

func GetConfigPlugin() *ConfigPlugin {
	return cp
}

func newDefaultConfig(configName string) {
	c := &ConfigPlugin{}
	c.vi = createViper(configName)
	cp = c
}
