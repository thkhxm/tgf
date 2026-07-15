package admin

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/thkhxm/tgf/v2"
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

var (
	monitorSecondMu    sync.Mutex
	monitorSecondCache []nodeSecondData
)

type nodeSecondData struct {
	s string
	d *NodeMonitorData
}

func AddSecondMonitor(all NodeMonitorData) {
	monitorSecondMu.Lock()
	defer monitorSecondMu.Unlock()

	monitorSecondCache = append(monitorSecondCache, nodeSecondData{
		s: time.Now().Format("2006-01-02 15:04:05"),
		d: &all,
	})
	if len(monitorSecondCache) > 50000 {
		monitorSecondCache = monitorSecondCache[30000:]
	}
}

var (
	monitorMu    sync.RWMutex
	monitorCache = make(map[string]map[string]*KeyValueMonitor)
)

func getMonitor(group, key string) *KeyValueMonitor {
	monitorMu.RLock()
	groupMonitors := monitorCache[group]
	monitor := groupMonitors[key]
	monitorMu.RUnlock()
	if monitor != nil {
		return monitor
	}

	monitorMu.Lock()
	defer monitorMu.Unlock()
	groupMonitors = monitorCache[group]
	if groupMonitors == nil {
		groupMonitors = make(map[string]*KeyValueMonitor)
		monitorCache[group] = groupMonitors
	}
	if monitor = groupMonitors[key]; monitor == nil {
		monitor = &KeyValueMonitor{key: key}
		groupMonitors[key] = monitor
	}
	return monitor
}

func AllMonitor() NodeMonitorData {
	monitorMu.RLock()
	defer monitorMu.RUnlock()

	res := make([]MonitorData, 0, len(monitorCache))
	for group, monitors := range monitorCache {
		values := make([]MonitorItem, 0, len(monitors))
		for _, monitor := range monitors {
			values = append(values, MonitorItem{
				Key:   monitor.key,
				Count: monitor.total.Load(),
			})
		}
		res = append(res, MonitorData{Values: values, Group: group})
	}
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

func writeJSONResponse(writer http.ResponseWriter, value any) error {
	jsonData, err := sonic.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal JSON response: %w", err)
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if _, err = writer.Write(jsonData); err != nil {
		return fmt.Errorf("write JSON response: %w", err)
	}
	return nil
}

func QueryMonitor(writer http.ResponseWriter, _ *http.Request) {
	if err := writeJSONResponse(writer, AllMonitor()); err != nil {
		log.Printf("query monitor response failed: %v", err)
	}
}
