package db

// C4 / A1b · FailureQueue 单测
//
// 覆盖：
//  1. NoopFailureQueue 所有方法不 panic
//  2. MemoryFailureQueue 的 Enqueue/Drain/Len 顺序和并发
//  3. FileFailureQueue 跨进程重启：启动时能读到上一次进程留下的条目
//  4. encode/decode 对称
//  5. ReplayFailureQueue 成功路径 + 失败时剩余条目重新入队

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNoopFailureQueue_IsHarmless(t *testing.T) {
	q := NoopFailureQueue{}
	if err := q.Enqueue(FailurePayload("x")); err != nil {
		t.Fatal(err)
	}
	out, err := q.Drain()
	if err != nil || len(out) != 0 {
		t.Fatalf("期望空 drain, 实际 %v err=%v", out, err)
	}
	if q.Len() != 0 {
		t.Error("Len 应为 0")
	}
	if err := q.Close(); err != nil {
		t.Error(err)
	}
}

func TestMemoryFailureQueue_OrderingAndDrain(t *testing.T) {
	q := NewMemoryFailureQueue()
	for i := 0; i < 5; i++ {
		if err := q.Enqueue(FailurePayload(fmt.Sprintf("p%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if q.Len() != 5 {
		t.Fatalf("Len 期望 5, 实际 %d", q.Len())
	}

	out, err := q.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 5 {
		t.Fatalf("drain 期望 5 条, 实际 %d", len(out))
	}
	for i, p := range out {
		if string(p) != fmt.Sprintf("p%d", i) {
			t.Fatalf("顺序错 idx=%d got=%s", i, string(p))
		}
	}
	if q.Len() != 0 {
		t.Error("drain 后应为空")
	}
}

func TestMemoryFailureQueue_Concurrent(t *testing.T) {
	q := NewMemoryFailureQueue()
	var wg sync.WaitGroup
	const (
		workers = 20
		each    = 100
	)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < each; j++ {
				_ = q.Enqueue(FailurePayload(fmt.Sprintf("w%d-%d", id, j)))
			}
		}(i)
	}
	wg.Wait()
	if q.Len() != workers*each {
		t.Errorf("并发 enqueue 期望 %d, 实际 %d", workers*each, q.Len())
	}
}

func TestMemoryFailureQueue_EnqueueCopiesInput(t *testing.T) {
	q := NewMemoryFailureQueue()
	buf := FailurePayload("original")
	_ = q.Enqueue(buf)
	// 篡改原 buf
	buf[0] = 'X'
	out, _ := q.Drain()
	if string(out[0]) != "original" {
		t.Errorf("Enqueue 应当拷贝输入防止污染, 实际 %s", string(out[0]))
	}
}

func TestFileFailureQueue_PersistsAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "failures.log")

	// 第一个进程：写入 3 条
	q1, err := NewFileFailureQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := q1.Enqueue(FailurePayload(fmt.Sprintf("first-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	_ = q1.Close()

	// 第二个进程重新打开：应当看到 3 条未 drain 的数据
	q2, err := NewFileFailureQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q2.Close()
	if q2.Len() != 3 {
		t.Errorf("重开后 Len 期望 3, 实际 %d", q2.Len())
	}
	out, err := q2.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("drain 期望 3 条, 实际 %d", len(out))
	}
	for i, p := range out {
		expected := fmt.Sprintf("first-%d", i)
		if string(p) != expected {
			t.Errorf("顺序错 idx=%d got=%s want=%s", i, string(p), expected)
		}
	}

	// Drain 之后 Len 应当归零
	if q2.Len() != 0 {
		t.Error("drain 后 Len 应为 0")
	}

	// 再写一条，确认 Drain 之后文件仍然可以 append
	if err := q2.Enqueue(FailurePayload("after-drain")); err != nil {
		t.Fatal(err)
	}
	out2, _ := q2.Drain()
	if len(out2) != 1 || string(out2[0]) != "after-drain" {
		t.Errorf("post-drain enqueue 读取错 %v", out2)
	}
}

func TestFileFailureQueue_ClosedRejectsOps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "closed.log")
	q, err := NewFileFailureQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = q.Close()

	if err := q.Enqueue(FailurePayload("x")); err == nil {
		t.Error("closed queue Enqueue 应报错")
	}
	if _, err := q.Drain(); err == nil {
		t.Error("closed queue Drain 应报错")
	}
	// Close 幂等
	if err := q.Close(); err != nil {
		t.Errorf("重复 Close 应幂等, 实际 err: %v", err)
	}
}

func TestNewFileFailureQueue_NonExistentPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "does-not-exist.log")
	_, err := NewFileFailureQueue(path)
	if err == nil {
		t.Fatal("父目录不存在应当返回错误")
	}
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0o755)
}

// --- payload encode/decode ---

func TestEncodeDecodeFailurePayload_RoundTrip(t *testing.T) {
	original := []any{
		"hello",
		int64(42),
		float64(3.14),
		true,
	}
	raw, err := encodeFailurePayload("users", original, 4)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := DecodeFailurePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Table != "users" {
		t.Errorf("table 期望 users, 实际 %v", doc.Table)
	}
	if doc.Count != 4 {
		t.Errorf("count 期望 4, 实际 %v", doc.Count)
	}
	if len(doc.Values) != 4 {
		t.Fatalf("values 期望 4 个, 实际 %d", len(doc.Values))
	}
	if doc.Values[0] != "hello" {
		t.Errorf("value[0] 错 %v", doc.Values[0])
	}
}

func TestDecodeFailurePayload_EmptyReturnsError(t *testing.T) {
	_, err := DecodeFailurePayload(nil)
	if err == nil {
		t.Error("空 payload 应报错")
	}
	_, err = DecodeFailurePayload(FailurePayload("not json"))
	if err == nil {
		t.Error("坏 JSON 应报错")
	}
}

// --- ReplayFailureQueue ---

func TestReplayFailureQueue_Success(t *testing.T) {
	q := NewMemoryFailureQueue()
	_ = q.Enqueue(FailurePayload("a"))
	_ = q.Enqueue(FailurePayload("b"))
	_ = q.Enqueue(FailurePayload("c"))

	var seen []string
	err := ReplayFailureQueue(q, func(p FailurePayload) error {
		seen = append(seen, string(p))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || seen[0] != "a" || seen[2] != "c" {
		t.Errorf("replay 顺序错 %v", seen)
	}
	if q.Len() != 0 {
		t.Error("成功 replay 后队列应为空")
	}
}

func TestReplayFailureQueue_FailureRequeuesRemaining(t *testing.T) {
	q := NewMemoryFailureQueue()
	_ = q.Enqueue(FailurePayload("a"))
	_ = q.Enqueue(FailurePayload("b"))
	_ = q.Enqueue(FailurePayload("c"))

	var seen []string
	err := ReplayFailureQueue(q, func(p FailurePayload) error {
		seen = append(seen, string(p))
		if string(p) == "b" {
			return fmt.Errorf("boom")
		}
		return nil
	})
	if err == nil {
		t.Fatal("flushFn 失败应返回错误")
	}
	if len(seen) != 2 || seen[0] != "a" || seen[1] != "b" {
		t.Errorf("期望看到 a 和 b, 实际 %v", seen)
	}
	// 剩余 b（失败点）+ c 应当重新入队
	if q.Len() != 2 {
		t.Errorf("剩余应有 2 条, 实际 %d", q.Len())
	}
	out, _ := q.Drain()
	if string(out[0]) != "b" || string(out[1]) != "c" {
		t.Errorf("重新入队顺序错 %v", out)
	}
}

func TestReplayFailureQueue_NilArgsSafe(t *testing.T) {
	// 任一 nil 都应当 no-op 返回 nil
	if err := ReplayFailureQueue(nil, func(FailurePayload) error { return nil }); err != nil {
		t.Error(err)
	}
	if err := ReplayFailureQueue(NewMemoryFailureQueue(), nil); err != nil {
		t.Error(err)
	}
}

// --- 与 autoCacheManager 的集成 ---

func TestResolveFailureQueue_FallsBackToDefaultFile(t *testing.T) {
	// E4：默认实现从 Noop 升级为进程级 File 队列（详细断言见
	// e4_replay_test.go TestResolveFailureQueue_DefaultIsFileQueue）。
	swapDefaultFailureQueueForTest(t, filepath.Join(t.TempDir(), "fallback.log"))
	a := &autoCacheManager[string, int]{builder: &AutoCacheBuilder[string, int]{}}
	q := a.resolveFailureQueue()
	if _, ok := q.(NoopFailureQueue); ok {
		t.Errorf("E4 后未配置不应再 fallback 到 Noop, 实际 %T", q)
	}
	if _, ok := q.(*FileFailureQueue); !ok {
		t.Errorf("未配置时应 fallback 到默认 File 队列, 实际 %T", q)
	}
}

func TestResolveFailureQueue_UsesBuilderValue(t *testing.T) {
	mem := NewMemoryFailureQueue()
	a := &autoCacheManager[string, int]{
		builder: &AutoCacheBuilder[string, int]{longevityFailureQueue: mem},
	}
	q := a.resolveFailureQueue()
	if q != FailureQueue(mem) {
		t.Error("应返回 builder 注入的 queue")
	}
}
