package common

import (
	"tframework.com/server/common/internal/define"
)

//***************************************************
//author tim.huang
//2022/8/11
//
//
//***************************************************

type ModuleName string

const (
	Chat ModuleName = "Chat"
)

func GetAddress() string {
	return *define.Address
}

func GetCallDepth() int {
	return *define.CallDepth
}

func GetModules() string {
	return *define.Modules
}

func GetPort() int {
	return *define.Port
}

func GetConfigPath() string {
	return *define.ConfigPath
}
