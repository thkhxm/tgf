package db

// E3-db · 埋点行为测试：用 metrics.NewMemoryProvider 断言落库批次/失败数/
// 补偿队列深度/replay 等关键点位真实生效（Provider 默认 NoOp，埋点不生效
// 的回归会在这里被抓出来）。

import (
	"errors"
	"testing"

	"github.com/thkhxm/tgf/v2/metrics"
)

// memProviderForTest 安装内存 Provider 并在测试结束恢复原 Provider。
func installMemoryMetrics(t *testing.T) interface {
	CounterValue(name string) float64
	GaugeValue(name string) float64
} {
	t.Helper()
	old := metrics.GetProvider()
	mp := metrics.NewMemoryProvider()
	metrics.SetProvider(mp)
	t.Cleanup(func() { metrics.SetProvider(old) })
	return mp
}

func TestFlushMetrics_FailurePathCounters(t *testing.T) {
	mp := installMemoryMetrics(t)

	m := newTestManager(2) // 3 条 groupSize=2 → 2 批
	seedDirty(m, 3)
	q := NewMemoryFailureQueue()
	m.builder.longevityFailureQueue = q
	m.flushBatch = func(values []any, count int) error { return errors.New("mysql down") }

	m.toLongevity()

	if got := mp.CounterValue(metricDBFlushBatchTotal); got != 2 {
		t.Errorf("%s 期望 2，实际 %v", metricDBFlushBatchTotal, got)
	}
	if got := mp.CounterValue(metricDBFlushBatchFailTotal); got != 2 {
		t.Errorf("%s 期望 2，实际 %v", metricDBFlushBatchFailTotal, got)
	}
	if got := mp.CounterValue(metricDBFlushRowsFailTotal); got != 3 {
		t.Errorf("%s 期望 3，实际 %v", metricDBFlushRowsFailTotal, got)
	}
	if got := mp.CounterValue(metricDBFailureEnqueueTotal); got != 2 {
		t.Errorf("%s 期望 2，实际 %v", metricDBFailureEnqueueTotal, got)
	}
	if got := mp.GaugeValue(metricDBFailureQueueDepth); got != float64(q.Len()) || got != 2 {
		t.Errorf("%s 期望 2（与 q.Len() 一致），实际 %v", metricDBFailureQueueDepth, got)
	}
}

func TestFlushMetrics_SuccessPathCounters(t *testing.T) {
	mp := installMemoryMetrics(t)

	m := newTestManager(2) // 3 条 → 2 批
	seedDirty(m, 3)
	m.flushBatch = func(values []any, count int) error { return nil }

	m.toLongevity()

	if got := mp.CounterValue(metricDBFlushBatchTotal); got != 2 {
		t.Errorf("%s 期望 2，实际 %v", metricDBFlushBatchTotal, got)
	}
	if got := mp.CounterValue(metricDBFlushRowsSuccessTotal); got != 3 {
		t.Errorf("%s 期望 3，实际 %v", metricDBFlushRowsSuccessTotal, got)
	}
	if got := mp.CounterValue(metricDBFlushBatchFailTotal); got != 0 {
		t.Errorf("%s 期望 0，实际 %v", metricDBFlushBatchFailTotal, got)
	}
}

func TestReplayMetrics_SuccessAndDepth(t *testing.T) {
	mp := installMemoryMetrics(t)

	q := NewMemoryFailureQueue()
	p, err := encodeFailurePayload("e3_replay_tbl", []any{"a", "b"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(p); err != nil {
		t.Fatal(err)
	}

	var flushedCount int
	registerReplayFlusher("e3_replay_tbl", func(values []any, count int) error {
		flushedCount += count
		return nil
	})

	replayed, requeued, rerr := replayQueueForKnownTables(q)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if replayed != 1 || requeued != 0 || flushedCount != 1 {
		t.Fatalf("期望 replayed=1 requeued=0 flushed=1，实际 %d/%d/%d", replayed, requeued, flushedCount)
	}
	if got := mp.CounterValue(metricDBFailureReplayTotal); got != 1 {
		t.Errorf("%s 期望 1，实际 %v", metricDBFailureReplayTotal, got)
	}
	if got := mp.GaugeValue(metricDBFailureQueueDepth); got != 0 {
		t.Errorf("replay 后 %s 期望 0，实际 %v", metricDBFailureQueueDepth, got)
	}
}

func TestReplayMetrics_FailureCounter(t *testing.T) {
	mp := installMemoryMetrics(t)

	q := NewMemoryFailureQueue()
	p, _ := encodeFailurePayload("e3_replay_fail_tbl", []any{"a", "b"}, 1)
	_ = q.Enqueue(p)
	registerReplayFlusher("e3_replay_fail_tbl", func(values []any, count int) error {
		return errors.New("still down")
	})

	_, _, rerr := replayQueueForKnownTables(q)
	if rerr == nil {
		t.Fatalf("落库失败应返回 error")
	}
	if got := mp.CounterValue(metricDBFailureReplayFail); got != 1 {
		t.Errorf("%s 期望 1，实际 %v", metricDBFailureReplayFail, got)
	}
	if got := mp.GaugeValue(metricDBFailureQueueDepth); got != 1 {
		t.Errorf("失败重入队后 %s 期望 1，实际 %v", metricDBFailureQueueDepth, got)
	}
}
