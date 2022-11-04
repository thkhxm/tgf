package tlog

import (
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/internal/plugin"
)

//***************************************************
//author tim.huang
//2022/11/4
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

// ***********************    struct    ****************************
type TLogService struct {
	l *plugin.TLogPlugin
}

//***********************    struct_end    ****************************

func (this *TLogService) Info(format string, v ...interface{}) {
	this.l.Info(format, v)
}

func (this *TLogService) Debug(format string, v ...interface{}) {
	this.l.Debug(format, v)
}

func (this *TLogService) Warning(format string, v ...interface{}) {
	this.l.Warning(format, v)
}

func (this *TLogService) WarningS(format string, v ...interface{}) {
	this.l.WarningS(format, v)
}

func (this *TLogService) InfoS(format string, v ...interface{}) {
	this.l.InfoS(format, v)
}

func (this *TLogService) DebugS(format string, v ...interface{}) {
	this.l.DebugS(format, v)
}

func NewTLogService(l *plugin.TLogPlugin) tframework.ILogService {
	t := new(TLogService)
	t.l = l
	return t
}

func init() {
}
