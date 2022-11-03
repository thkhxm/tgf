package tframework

import "github.com/spf13/viper"

//***************************************************
//author tim.huang
//2022/8/18
//
//
//***************************************************

type IConfigPlugin interface {
	GetVI() *viper.Viper
}
