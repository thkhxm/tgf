package plugin

import (
	"fmt"
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
