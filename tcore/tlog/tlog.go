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
	plugin.GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.BlueString("\t[I] [Server]"), format), v...)
}

// WarningS
// @Description: 服务器Warning级别日志
// @param format
// @param v
func WarningS(format string, v ...interface{}) {
	plugin.GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.YellowString("\t[W] [Server]"), format), v...)
}

func Info(format string, v ...interface{}) {
	plugin.GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.BlueString("\t[I] [Logic]"), format), v...)
}

func Debug(format string, v ...interface{}) {
	plugin.GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.GreenString("\t[D] [Logic]"), format), v...)
}

func Warning(format string, v ...interface{}) {
	plugin.GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.YellowString("\t[W] [Logic]"), format), v...)
}
func init() {
}
