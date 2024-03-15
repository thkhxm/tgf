package rpc

import (
	"context"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/exp/admin"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/2/26
//***************************************************

type MonitorService struct {
	Module
}

func (m *MonitorService) ASyncMonitor(ctx context.Context, args *string, reply *admin.NodeMonitorData) (err error) {
	all := admin.AllMonitor()
	*reply = all
	return
}

func (m *MonitorService) GetName() string {
	return tgf.MonitorServiceModuleName
}

func (m *MonitorService) GetVersion() string {
	return "1.0"
}

func (m *MonitorService) Startup() (bool, error) {
	var ()
	return true, nil
}
