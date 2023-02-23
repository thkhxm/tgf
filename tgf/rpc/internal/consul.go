package internal

import (
	"fmt"
	"github.com/rcrowley/go-metrics"
	"github.com/rpcxio/rpcx-consul/serverplugin"
	"github.com/smallnest/rpcx/server"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

type ConsulDiscovery struct {
}

func (this *ConsulDiscovery) RegisterServer() server.Plugin {
	var ()
	address := tgf.GetConsulAddress()
	r := &serverplugin.ConsulRegisterPlugin{
		ServiceAddress: fmt.Sprintf("tcp@%v:%v", util.GetLocalHost(), tgf.GetServicePort()),
		ConsulServers:  address,
		BasePath:       tgf.GetConsulPath(),
		Metrics:        metrics.NewRegistry(),
		UpdateInterval: time.Second * 11,
	}
	err := r.Start()
	if err != nil {
		log.Error("服务发现启动异常 %v", err)
	}
	return this
}

func NewConsulDiscovery() IRPCDiscovery {
	return nil
}
