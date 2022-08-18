package tlog

import (
	"fmt"
	"tframework.com/rpc/tcore/internal/plugin"
)

//***************************************************
//author tim.huang
//2022/8/17
//
//
//***************************************************

func ServerLogF(format string, v ...interface{}) {
	plugin.GetLogPlugin().FInfo(fmt.Sprintf("[Server] %v", format), v...)
}

func init() {
}
