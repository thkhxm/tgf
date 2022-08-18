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

var l = newDefaultLogger()

// os.Stdout, "", slog.LstdFlags|slog.Lshortfile, log.LvInfo
type TLogPlugin struct {
	BasePlugin
	*log.Logger
}

func init() {

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
	lp.Output(*common.CallDepth, msg)
}

func (lp *TLogPlugin) FInfo(msg string, params ...interface{}) {
	lp.Output(*common.CallDepth, fmt.Sprintf(msg, params...))
}
