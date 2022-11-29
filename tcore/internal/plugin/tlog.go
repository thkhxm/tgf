package plugin

import (
	"fmt"
	"github.com/fatih/color"
	"log"
	"os"
	"tframework.com/rpc/tcore/internal/define"
)

// ***************************************************
// author tim.huang
// 2022/8/17
//
// ***************************************************
var l *TLogPlugin

// os.Stdout, "", slog.LstdFlags|slog.Lshortfile, log.LvInfo
type TLogPlugin struct {
	*log.Logger
}

func init() {

}

func NewDefaultLogger() *TLogPlugin {
	lo := &TLogPlugin{}
	lo.Logger = log.New(os.Stdout, "", log.LstdFlags|log.Lshortfile)
	l = lo
	return lo
}

func (lp *TLogPlugin) info(msg string) {
	lp.Output(*define.CallDepth, msg)
}

func (lp *TLogPlugin) fInfo(msg string, params ...interface{}) {
	lp.Output(*define.CallDepth, fmt.Sprintf(msg, params...))
}

func (lp *TLogPlugin) fInfoInsert(msg string, params ...interface{}) {
	lp.Output(*define.CallDepth-1, fmt.Sprintf(msg, params...))
}

func (lp *TLogPlugin) Info(format string, v ...interface{}) {
	lp.fInfo(fmt.Sprintf("%v %v", color.BlueString("\t[I] [Logic]"), format), v...)
}

func (lp *TLogPlugin) Debug(format string, v ...interface{}) {
	lp.fInfo(fmt.Sprintf("%v %v", color.GreenString("\t[D] [Logic]"), format), v...)
}

func (lp *TLogPlugin) Warning(format string, v ...interface{}) {
	lp.fInfo(fmt.Sprintf("%v %v", color.YellowString("\t[W] [Logic]"), format), v...)
}
func (lp *TLogPlugin) Error(format string, v ...interface{}) {
	lp.fInfo(fmt.Sprintf("%v %v", color.RedString("\t[E] [Logic]"), format), v...)
}

func (lp *TLogPlugin) ErrorS(format string, v ...interface{}) {
	lp.fInfo(fmt.Sprintf("%v %v", color.RedString("\t[E] [Server]"), format), v...)
}
func (lp *TLogPlugin) WarningS(format string, v ...interface{}) {
	lp.fInfo(fmt.Sprintf("%v %v", color.YellowString("\t[W] [Server]"), format), v...)
}

func (lp *TLogPlugin) InfoS(format string, v ...interface{}) {
	lp.fInfo(fmt.Sprintf("%v %v", color.BlueString("\t[I] [Server]"), format), v...)
}

func (lp *TLogPlugin) DebugS(format string, v ...interface{}) {
	lp.fInfo(fmt.Sprintf("%v %v", color.WhiteString("\t[D] [Server]"), format), v...)
}
func InfoS(format string, v ...interface{}) {
	l.fInfoInsert(fmt.Sprintf("%v %v", color.BlueString("\t[I] [Server]"), format), v...)
}
func init() {
}
