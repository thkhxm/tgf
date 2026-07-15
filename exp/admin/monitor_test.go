package admin

import (
	"errors"
	"net/http"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/2/26
//***************************************************

func Test_getMonitor(t *testing.T) {
	a := getMonitor("a", "b")
	if a == nil {
		t.Fatal("getMonitor returned nil")
	}
}

func TestAllMonitorKeepsGroupsIndependent(t *testing.T) {
	monitorMu.Lock()
	previous := monitorCache
	monitorCache = make(map[string]map[string]*KeyValueMonitor)
	monitorMu.Unlock()
	t.Cleanup(func() {
		monitorMu.Lock()
		monitorCache = previous
		monitorMu.Unlock()
	})

	getMonitor("group-a", "a").Inr()
	getMonitor("group-b", "b").Inr()

	all := AllMonitor()
	if len(all.Data) != 2 {
		t.Fatalf("group count = %d, want 2", len(all.Data))
	}
	for _, group := range all.Data {
		if len(group.Values) != 1 {
			t.Errorf("group %q contains %d values, want 1: %+v", group.Group, len(group.Values), group.Values)
		}
	}
}

type failingResponseWriter struct {
	err error
}

func (w *failingResponseWriter) Header() http.Header { return make(http.Header) }
func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, w.err
}
func (w *failingResponseWriter) WriteHeader(int) {}

func TestResponseHelpersReturnMarshalAndWriteErrors(t *testing.T) {
	if err := writeJSONResponse(&failingResponseWriter{}, make(chan int)); err == nil {
		t.Fatal("writeJSONResponse should return a marshal error for channels")
	}

	want := errors.New("injected writer failure")
	writer := &failingResponseWriter{err: want}
	if err := writeJSONResponse(writer, NodeMonitorData{}); !errors.Is(err, want) {
		t.Fatalf("writeJSONResponse error = %v, want wrapped %v", err, want)
	}
	if err := writeResult(writer, "fail"); !errors.Is(err, want) {
		t.Fatalf("writeResult error = %v, want wrapped %v", err, want)
	}
}
