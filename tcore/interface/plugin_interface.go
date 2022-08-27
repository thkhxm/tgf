package tframework

import "github.com/spf13/viper"

//***************************************************
//author tim.huang
//2022/8/18
//
//
//***************************************************

type ILogPlugin interface {
	Info(msg string)
	FInfo(msg string, params ...interface{})
}
type IConfigPlugin interface {
	GetVI() *viper.Viper
}
