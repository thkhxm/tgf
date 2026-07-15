package rpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thkhxm/tgf/v2/exp/admin"
)

type cancelAwareMonitorXClient struct {
	nilCallXClient
	called       chan struct{}
	canceled     chan struct{}
	calledOnce   sync.Once
	canceledOnce sync.Once
}

func newCancelAwareMonitorXClient() *cancelAwareMonitorXClient {
	return &cancelAwareMonitorXClient{
		called:   make(chan struct{}),
		canceled: make(chan struct{}),
	}
}

func (c *cancelAwareMonitorXClient) Call(ctx context.Context, _ string, _, _ interface{}) error {
	c.calledOnce.Do(func() { close(c.called) })
	<-ctx.Done()
	c.canceledOnce.Do(func() { close(c.canceled) })
	return ctx.Err()
}

func TestAdminMonitorCallHasDeadline(t *testing.T) {
	xc := newCancelAwareMonitorXClient()
	restore := withStubRPCClient(t, "monitor", xc)
	defer restore()
	a := &Admin{
		monitorInterval:  time.Millisecond,
		monitorCallLimit: 20 * time.Millisecond,
		monitorStopLimit: time.Second,
	}
	if ok, err := a.Startup(); !ok || err != nil {
		t.Fatalf("Startup() = (%v, %v), want (true, nil)", ok, err)
	}
	defer a.Destroy(nil)
	select {
	case <-xc.canceled:
	case <-time.After(time.Second):
		t.Fatal("monitor RPC did not honor its call deadline")
	}
}

func TestAdminDestroyCancelsAndJoinsMonitor(t *testing.T) {
	xc := newCancelAwareMonitorXClient()
	restore := withStubRPCClient(t, "monitor", xc)
	defer restore()
	a := &Admin{
		monitorInterval:  time.Millisecond,
		monitorCallLimit: time.Minute,
		monitorStopLimit: time.Second,
	}
	if ok, err := a.Startup(); !ok || err != nil {
		t.Fatalf("Startup() = (%v, %v), want (true, nil)", ok, err)
	}
	select {
	case <-xc.called:
	case <-time.After(time.Second):
		t.Fatal("monitor RPC did not start")
	}

	start := time.Now()
	a.Destroy(nil)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Destroy() did not join the canceled monitor promptly: %v", elapsed)
	}
	select {
	case <-xc.canceled:
	default:
		t.Fatal("Destroy() returned before the in-flight monitor observed cancellation")
	}
	a.monitorMu.Lock()
	defer a.monitorMu.Unlock()
	if a.autoUpdateTicker != nil || a.autoUpdateCancel != nil || a.autoUpdateDone != nil {
		t.Fatal("Destroy() retained monitor lifecycle state")
	}
}

func TestAdminDestroyStartupInterleavingIsTerminal(t *testing.T) {
	for i := 0; i < 100; i++ {
		a := &Admin{monitorInterval: time.Hour, monitorStopLimit: time.Second}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = a.Startup()
		}()
		go func() {
			defer wg.Done()
			a.Destroy(nil)
		}()
		wg.Wait()

		if ok, err := a.Startup(); ok || !errors.Is(err, errAdminDestroyed) {
			t.Fatalf("Startup() after Destroy = (%v, %v), want (false, %v)", ok, err, errAdminDestroyed)
		}
		a.monitorMu.Lock()
		if !a.monitorDestroyed || a.autoUpdateTicker != nil || a.autoUpdateCancel != nil || a.autoUpdateDone != nil {
			a.monitorMu.Unlock()
			t.Fatal("Destroy/Startup interleaving resurrected monitor lifecycle state")
		}
		a.monitorMu.Unlock()
	}
}

func TestMergeNodeMonitorDataAccumulatesGroupsAndKeys(t *testing.T) {
	dst := admin.NodeMonitorData{Data: []admin.MonitorData{
		{Group: "rpc", Values: []admin.MonitorItem{{Key: "login", Count: 2}}},
	}}
	mergeNodeMonitorData(&dst, admin.NodeMonitorData{Data: []admin.MonitorData{
		{Group: "rpc", Values: []admin.MonitorItem{{Key: "login", Count: 3}, {Key: "move", Count: 4}}},
		{Group: "db", Values: []admin.MonitorItem{{Key: "flush", Count: 5}}},
	}})

	if len(dst.Data) != 2 {
		t.Fatalf("group count = %d, want 2", len(dst.Data))
	}
	if got := dst.Data[0].Values[0].Count; got != 5 {
		t.Fatalf("rpc.login count = %d, want 5", got)
	}
	if len(dst.Data[0].Values) != 2 || dst.Data[0].Values[1].Key != "move" || dst.Data[0].Values[1].Count != 4 {
		t.Fatalf("rpc values = %+v, want appended move=4", dst.Data[0].Values)
	}
	if dst.Data[1].Group != "db" || len(dst.Data[1].Values) != 1 || dst.Data[1].Values[0].Count != 5 {
		t.Fatalf("db group = %+v, want flush=5", dst.Data[1])
	}
}
