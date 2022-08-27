package plugin

import (
	"fmt"
	"github.com/fatih/color"
	"log"
	"os"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/server/common"
)

//***************************************************
//author tim.huang
//2022/8/17
//
//
//***************************************************

var l tframework.ILogPlugin

// os.Stdout, "", slog.LstdFlags|slog.Lshortfile, log.LvInfo
type TLogPlugin struct {
	BasePlugin
	*log.Logger
}

// DebugS
// @Description: 服务器Debug级别日志
// @param format
// @param v
func DebugS(format string, v ...interface{}) {
	GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.WhiteString("\t[D] [Server]"), format), v...)
}

// InfoS
// @Description: 服务器Info级别日志
// @param format
// @param v
func InfoS(format string, v ...interface{}) {
	GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.BlueString("\t[I] [Server]"), format), v...)
}

// WarningS
// @Description: 服务器Warning级别日志
// @param format
// @param v
func WarningS(format string, v ...interface{}) {
	GetLogPlugin().FInfo(fmt.Sprintf("%v %v", color.YellowString("\t[W] [Server]"), format), v...)
}

func init() {
	l = newDefaultLogger()
}

func GetLogPlugin() tframework.ILogPlugin {
	return l
}

func newDefaultLogger() tframework.ILogPlugin {
	lo := &TLogPlugin{}
	lo.Logger = log.New(os.Stdout, "", log.LstdFlags|log.Lshortfile)
	return lo
}

func (lp *TLogPlugin) Info(msg string) {
	lp.Output(common.GetCallDepth(), msg)
}

func (lp *TLogPlugin) FInfo(msg string, params ...interface{}) {
	lp.Output(common.GetCallDepth(), fmt.Sprintf(msg, params...))
}
