package internal

import (
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/rcrowley/go-metrics"
	"github.com/rpcxio/libkv/store"
	"github.com/rpcxio/rpcx-consul/client"
	"github.com/rpcxio/rpcx-consul/serverplugin"
	"github.com/smallnest/rpcx/server"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

// TODO 修改consul的配置，调整心跳间隔

type ConsulDiscovery struct {
	discoveryMap *hashmap.Map[string, *client.ConsulDiscovery]
}

func (c *ConsulDiscovery) initStruct() {
	var ()
	c.discoveryMap = hashmap.New[string, *client.ConsulDiscovery]()
}

func (c *ConsulDiscovery) RegisterServer(ip string) server.Plugin {
	var (
		address        = tgf.GetStrListConfig(tgf.EnvironmentConsulAddress)
		serviceAddress = fmt.Sprintf("tcp@%v", ip)
		_logAddressMsg string
		_basePath      = tgf.GetStrConfig[string](tgf.EnvironmentConsulPath)
	)
	//注册服务发现根目录
	r := &serverplugin.ConsulRegisterPlugin{
		ServiceAddress: serviceAddress,
		ConsulServers:  address,
		BasePath:       _basePath,
		Metrics:        metrics.NewRegistry(),
		UpdateInterval: time.Second * 10,
	}
	err := r.Start()
	if err != nil {
		log.Error("[init] 服务发现启动异常 %v", err)
	}
	for _, s := range address {
		_logAddressMsg += s + ","
	}
	log.InfoTag("init", "服务发现加载成功 注册根目录 consulAddress=%v serviceAddress=%v path=%v", r.ServiceAddress, _logAddressMsg, _basePath)

	return r
}

func (c *ConsulDiscovery) RegisterDiscovery(moduleName string) *client.ConsulDiscovery {
	var ()
	var (
		address  = tgf.GetStrListConfig(tgf.EnvironmentConsulAddress)
		basePath = tgf.GetStrConfig[string](tgf.EnvironmentConsulPath)
	)

	//new discovery

	conf := &store.Config{
		ClientTLS:         nil,
		TLS:               nil,
		ConnectionTimeout: 0,
		Bucket:            "",
		PersistConnection: false,
		Username:          "",
		Password:          "",
	}
	d, _ := client.NewConsulDiscovery(basePath, moduleName, address, conf)

	//if moduleName != "" {
	c.discoveryMap.Set(moduleName, d)
	log.InfoTag("init", "注册rpcx discovery moduleName=%v", moduleName)
	//}

	return d
}

func (c *ConsulDiscovery) GetDiscovery(moduleName string) *client.ConsulDiscovery {
	if val, ok := c.discoveryMap.Get(moduleName); ok {
		return val
	}
	return nil
}
