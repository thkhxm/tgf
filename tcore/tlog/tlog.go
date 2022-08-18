package tlog

import (
	"fmt"
	"github.com/fatih/color"
	"tframework.com/rpc/tcore/internal/plugin"
)

//***************************************************
//author tim.huang
//2022/8/17
//
//
//***************************************************

// InfoS
// @Description: 服务器Info级别日志
// @param format
// @param v
func InfoS(format string, v ...interface{}) {
	plugin.GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.BlueString("[I] [Server]"), format), v...)
}

// WarningS
// @Description: 服务器Warning级别日志
// @param format
// @param v
func WarningS(format string, v ...interface{}) {
	plugin.GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.YellowString("[W] [Server]"), format), v...)
}

func init() {
}
