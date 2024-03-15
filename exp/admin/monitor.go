package admin

import (
	"github.com/bytedance/sonic"
	"github.com/cornelk/hashmap"
	"github.com/thkhxm/rpcx/log"
	"github.com/thkhxm/tgf"
	"net/http"
	"sync/atomic"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/2/21
//***************************************************

const (
	TimeGroup10  = 10
	TimeGroup50  = 51
	TimeGroup100 = 102
	TimeGroup300 = 303
	TimeGroupMax = 1 << 30
)

const (
	ServiceMonitor = "service_monitor"
)

var monitorSecondCache []nodeSecondData

type nodeSecondData struct {
	s string
	d *NodeMonitorData
}

func AddSecondMonitor(all NodeMonitorData) {
	t := time.Now().Format("2011-01-01 21:01:00")

	monitorSecondCache = append(monitorSecondCache, nodeSecondData{
		s: t,
		d: &all,
	})
	if len(monitorSecondCache) > 50000 {
		monitorSecondCache = monitorSecondCache[30000:]
	}
}

var monitorCache = hashmap.New[string, []*KeyValueMonitor]()

func getMonitor(group, key string) (res *KeyValueMonitor) {
	if r, ok := monitorCache.Get(group); ok {
		for _, monitor := range r {
			if monitor.key == key {
				return monitor
			}
		}
		r = append(r, &KeyValueMonitor{
			key:   key,
			total: atomic.Int64{},
		})
		monitorCache.Set(group, r)
	} else {
		monitorCache.Set(group, make([]*KeyValueMonitor, 0, 100))
	}
	return getMonitor(group, key)
}

func AllMonitor() NodeMonitorData {
	sqd := make([]MonitorItem, 0)
	res := make([]MonitorData, 0)
	monitorCache.Range(func(group string, monitors []*KeyValueMonitor) bool {
		for _, monitor := range monitors {
			sqd = append(sqd, MonitorItem{
				Key:   monitor.key,
				Count: monitor.total.Load(),
			})
		}
		res = append(res, MonitorData{Values: sqd, Group: group})
		return true
	})
	return NodeMonitorData{
		NodeId: tgf.NodeId,
		Data:   res,
	}
}

type KeyValueMonitor struct {
	key   string
	total atomic.Int64
}

func (s *KeyValueMonitor) Inr() {
	s.total.Add(1)
}

func PointRPCRequest(module, serviceName string) {
	if serviceName == "ASyncMonitor" {
		return
	}
	m := getMonitor(ServiceMonitor, module+"."+serviceName)
	m.Inr()
	log.Info("monitor update")
}

type NodeMonitorData struct {
	NodeId string        `json:"nodeId"`
	Data   []MonitorData `json:"data"`
}

type MonitorData struct {
	Group  string        `json:"group"`
	Values []MonitorItem `json:"values"`
}

type MonitorItem struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

func QueryMonitor(writer http.ResponseWriter, request *http.Request) {
	//group := request.PathValue("group")
	data := AllMonitor()
	jsonData, _ := sonic.Marshal(data)
	writer.Write(jsonData)
}
