package tlog

import (
	"fmt"
	"github.com/fatih/color"
	"log"
	"os"
	tframework "tframework.com/rpc/tcore/interface"
)

//***************************************************
//author tim.huang
//2022/8/17
//
//
//***************************************************

var l ILogPlugin

type ILogPlugin interface {
	info(msg string)
	fInfo(msg string, params ...interface{})
}

// os.Stdout, "", slog.LstdFlags|slog.Lshortfile, log.LvInfo
type TLogPlugin struct {
	*log.Logger
}

func init() {
	l = newDefaultLogger()
}

func getLogPlugin() ILogPlugin {
	return l
}

func newDefaultLogger() ILogPlugin {
	lo := &TLogPlugin{}
	lo.Logger = log.New(os.Stdout, "", log.LstdFlags|log.Lshortfile)
	return lo
}

func (lp *TLogPlugin) info(msg string) {
	lp.Output(tframework.GetCallDepth(), msg)
}

func (lp *TLogPlugin) fInfo(msg string, params ...interface{}) {
	lp.Output(tframework.GetCallDepth(), fmt.Sprintf(msg, params...))
}

func Info(format string, v ...interface{}) {
	getLogPlugin().fInfo(fmt.Sprintf("%v %v", color.BlueString("\t[I] [Logic]"), format), v...)
}

func Debug(format string, v ...interface{}) {
	getLogPlugin().fInfo(fmt.Sprintf("%v %v", color.GreenString("\t[D] [Logic]"), format), v...)
}

func Warning(format string, v ...interface{}) {
	getLogPlugin().fInfo(fmt.Sprintf("%v %v", color.YellowString("\t[W] [Logic]"), format), v...)
}

func WarningS(format string, v ...interface{}) {
	getLogPlugin().fInfo(fmt.Sprintf("%v %v", color.YellowString("\t[W] [Server]"), format), v...)
}

func InfoS(format string, v ...interface{}) {
	getLogPlugin().fInfo(fmt.Sprintf("%v %v", color.BlueString("\t[I] [Server]"), format), v...)
}

func DebugS(format string, v ...interface{}) {
	getLogPlugin().fInfo(fmt.Sprintf("%v %v", color.WhiteString("\t[D] [Server]"), format), v...)
}

func init() {
}
