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
