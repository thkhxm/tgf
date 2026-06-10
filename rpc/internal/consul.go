package internal

import (
	"fmt"
	"github.com/cornelk/hashmap"
	"github.com/rcrowley/go-metrics"
	"github.com/rpcxio/libkv/store"
	"github.com/thkhxm/rpcx-consul/v2/client"
	"github.com/thkhxm/rpcx-consul/v2/serverplugin"
	"github.com/thkhxm/rpcx/v2/server"
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

var LocalServerAddress = ""

// ConsulDiscovery 是 rpcx-consul 的封装。
//
// A6 说明 discoveryMap 的语义：key 是 rpcx moduleName（集群里服务模块的有限集
// 合，通常几十个），不是会无限增长的动态集合。因此这里不需要 LRU/TTL 过期。
// RegisterDiscovery 对同一个 key 的重复调用会覆盖——这对幂等初始化是安全的，
// 但对"同一个 key 被重复注册导致浪费 conn"是略浪费的，所以 A6 顺手加了一层
// GetDiscovery 前置检查，已存在就复用。
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
	LocalServerAddress = serviceAddress
	return r
}

// newConsulDiscoveryFn 是 client.NewConsulDiscovery 的注入点。
// D5: 拆出来是为了让单测可以在无 Consul 的干净环境下注入桩实现，
// 覆盖"创建失败不缓存 nil / 失败后可重试 / 并发竞争 loser 关闭"等路径。
// 生产路径永远是真实的 client.NewConsulDiscovery。
var newConsulDiscoveryFn = client.NewConsulDiscovery

func (c *ConsulDiscovery) RegisterDiscovery(moduleName string) *client.ConsulDiscovery {
	// A6: 幂等化。同一 moduleName 重复调用直接返回已有实例，避免浪费 Consul 连接。
	// 原实现每次都 new 一个 ConsulDiscovery 并覆盖 map，被覆盖的老实例会泄漏一条
	// 长连接（直到 GC 把它回收，但在 watchBaseDiscovery 的 discovery.WatchService
	// 还持有 channel 的情况下 GC 不会生效）。
	if existing, ok := c.discoveryMap.Get(moduleName); ok && existing != nil {
		return existing
	}

	var (
		address  = tgf.GetStrListConfig(tgf.EnvironmentConsulAddress)
		basePath = tgf.GetStrConfig[string](tgf.EnvironmentConsulPath)
	)

	conf := &store.Config{
		ClientTLS:         nil,
		TLS:               nil,
		ConnectionTimeout: 0,
		Bucket:            "",
		PersistConnection: false,
		Username:          "",
		Password:          "",
	}
	d, err := newConsulDiscoveryFn(basePath, moduleName, address, conf)
	if err != nil || d == nil {
		// D5 / P0 吞错止血：原实现 `d, _ :=` 把错误吞掉，Consul 不可达时 nil 被
		// Insert 进 discoveryMap 永久占位——后续 GetDiscovery 恒返 nil，且每次
		// RegisterDiscovery 重新 new 的实例永远存不进 map（幂等检查只认 nil 条目），
		// 形成"先挂后恢复"场景下的永久故障 + 实例泄漏。
		// 现在：失败绝不写 map、记错误日志、返回 nil——下一次调用会重新尝试创建（可重试）。
		log.Error("[init] 创建rpcx discovery失败(不缓存,下次调用重试) moduleName=%v consulAddress=%v err=%v",
			moduleName, address, err)
		return nil
	}

	// 并发 RegisterDiscovery 同一个 moduleName 的情况下，两个 goroutine 都会走到
	// 这里——hashmap.Set 是原子的但后写者会覆盖先写者，可能产生一个无人引用的
	// ConsulDiscovery 泄漏。Insert 在已存在时不覆盖，所以先用它。
	if !c.discoveryMap.Insert(moduleName, d) {
		if existing, ok := c.discoveryMap.Get(moduleName); ok && existing != nil {
			// D5: 竞争失败的 loser 实例主动 Close（关闭 watch goroutine 与 kv 连接），
			// 修复 A6 审计指出的"loser 实例从不 Close 导致 stopCh/kv 连接泄漏"。
			d.Close()
			return existing
		}
	}
	log.InfoTag("init", "注册rpcx discovery moduleName=%v", moduleName)
	return d
}

func (c *ConsulDiscovery) GetDiscovery(moduleName string) *client.ConsulDiscovery {
	if val, ok := c.discoveryMap.Get(moduleName); ok {
		return val
	}
	return nil
}
