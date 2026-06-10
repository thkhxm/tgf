package db

// E4 · 补偿队列"默认 File + 启动 replay"端到端测试（不依赖真实 MySQL）。
//
// 端到端链路：
//   MySQL 宕机 → toLongevity 真实 flushBatch 失败 → 失败批次进默认 File 队列
//   →（模拟进程重启）新管理器创建触发 wireFailureReplay → payload 解码 →
//   按表名分发到 flushBatch → 真实走 execBatchOnce（记录型 fake driver 捕获
//   Exec 参数）→ 队列清空。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---- 记录型 SQL driver：捕获 Exec 的参数 ----

var e4Recorded struct {
	mu    sync.Mutex
	execs [][]driver.Value
}

func e4RecordedExecs() [][]driver.Value {
	e4Recorded.mu.Lock()
	defer e4Recorded.mu.Unlock()
	out := make([][]driver.Value, len(e4Recorded.execs))
	copy(out, e4Recorded.execs)
	return out
}

type e4RecordingDriver struct{}

func (e4RecordingDriver) Open(_ string) (driver.Conn, error) { return &e4RecConn{}, nil }

type e4RecConn struct{}

func (c *e4RecConn) Prepare(_ string) (driver.Stmt, error) { return &e4RecStmt{}, nil }
func (c *e4RecConn) Close() error                          { return nil }
func (c *e4RecConn) Begin() (driver.Tx, error)             { return e4RecTx{}, nil }
func (c *e4RecConn) BeginTx(_ context.Context, _ driver.TxOptions) (driver.Tx, error) {
	return e4RecTx{}, nil
}

type e4RecTx struct{}

func (e4RecTx) Commit() error   { return nil }
func (e4RecTx) Rollback() error { return nil }

type e4RecStmt struct{}

func (s *e4RecStmt) Close() error  { return nil }
func (s *e4RecStmt) NumInput() int { return -1 }
func (s *e4RecStmt) Exec(args []driver.Value) (driver.Result, error) {
	cp := make([]driver.Value, len(args))
	copy(cp, args)
	e4Recorded.mu.Lock()
	e4Recorded.execs = append(e4Recorded.execs, cp)
	e4Recorded.mu.Unlock()
	return driver.RowsAffected(int64(len(args))), nil
}
func (s *e4RecStmt) Query(_ []driver.Value) (driver.Rows, error) { return &e4RecRows{}, nil }

type e4RecRows struct{}

func (r *e4RecRows) Columns() []string           { return []string{} }
func (r *e4RecRows) Close() error                { return nil }
func (r *e4RecRows) Next(_ []driver.Value) error { return io.EOF }

func init() {
	sql.Register("tgf-e4-recording", e4RecordingDriver{})
}

// withRecordingDB 把包级 dbService 换成记录型 driver，并清空已记录的 Exec。
func withRecordingDB(t *testing.T) {
	t.Helper()
	d, err := sql.Open("tgf-e4-recording", "ok")
	if err != nil {
		t.Fatalf("open recording db: %v", err)
	}
	e4Recorded.mu.Lock()
	e4Recorded.execs = nil
	e4Recorded.mu.Unlock()
	old := dbService
	dbService = &mysqlService{running: true, db: d}
	t.Cleanup(func() {
		dbService = old
		_ = d.Close()
	})
}

// ---- 默认队列形态 ----

func TestResolveFailureQueue_DefaultIsFileQueue(t *testing.T) {
	swapDefaultFailureQueueForTest(t, filepath.Join(t.TempDir(), "default.log"))
	a := &autoCacheManager[string, int]{builder: &AutoCacheBuilder[string, int]{}}
	q := a.resolveFailureQueue()
	if _, ok := q.(*FileFailureQueue); !ok {
		t.Fatalf("E4: 未配置时默认补偿队列应为 File 实现（不再是 Noop），实际 %T", q)
	}
}

func TestResolveFailureQueue_ExplicitNoopOptsOut(t *testing.T) {
	a := &autoCacheManager[string, int]{
		builder: &AutoCacheBuilder[string, int]{longevityFailureQueue: NoopFailureQueue{}},
	}
	if _, ok := a.resolveFailureQueue().(NoopFailureQueue); !ok {
		t.Fatalf("显式注入 Noop 应关闭补偿队列")
	}
}

func TestSetDefaultFailureQueuePath(t *testing.T) {
	swapDefaultFailureQueueForTest(t, filepath.Join(t.TempDir(), "x.log"))
	custom := filepath.Join(t.TempDir(), "custom.log")
	if err := SetDefaultFailureQueuePath(custom); err != nil {
		t.Fatalf("首次使用前设置路径应成功: %v", err)
	}
	q := defaultFailureQueue()
	fq, ok := q.(*FileFailureQueue)
	if !ok {
		t.Fatalf("默认队列应为 File 实现，实际 %T", q)
	}
	if fq.path != custom {
		t.Errorf("默认队列路径应为 %s，实际 %s", custom, fq.path)
	}
	// 已初始化后再改路径必须报错
	if err := SetDefaultFailureQueuePath(filepath.Join(t.TempDir(), "late.log")); err == nil {
		t.Errorf("默认队列初始化后修改路径应返回 error")
	}
	if err := SetDefaultFailureQueuePath(""); err == nil {
		t.Errorf("空路径应返回 error")
	}
}

// ---- 端到端：MySQL 宕机 → 进队列 → 恢复后启动 replay 真实落库 ----

func TestFailureQueue_EndToEnd_MySQLDownThenRecoverReplay(t *testing.T) {
	swapDefaultFailureQueueForTest(t, filepath.Join(t.TempDir(), "e2e.log"))

	// 阶段 1：MySQL 宕机，周期 flush 走真实 flushBatch → 失败批次进默认 File 队列
	withDownMySQL(t)
	m1 := newTestManager(10)
	m1.sb.tableName = "e4_replay_tbl"
	m1.sb.retryAttempts = 1 // 加速：单次失败即进队列
	items := seedDirty(m1, 2)
	m1.flushBatch = m1.sb.flushBatch

	m1.toLongevity()

	q := defaultFailureQueue()
	if got := q.Len(); got != 1 {
		t.Fatalf("MySQL 宕机后失败批次应进默认补偿队列，期望 1 条 payload，实际 %d", got)
	}
	for i, cd := range items {
		if !cd.checkState(data_update) {
			t.Fatalf("item %d 脏标志应保留", i)
		}
	}

	// 阶段 2：模拟进程重启 + MySQL 恢复——新管理器创建即触发 wireFailureReplay，
	// 队列里的失败批次通过该表的 flushBatch 真实落库（记录型 driver 捕获 Exec）。
	withRecordingDB(t)
	m2 := newTestManager(10)
	m2.sb.tableName = "e4_replay_tbl"
	m2.flushBatch = m2.sb.flushBatch

	m2.wireFailureReplay()

	execs := e4RecordedExecs()
	if len(execs) != 1 {
		t.Fatalf("启动 replay 应触发 1 次真实 upsert Exec，实际 %d", len(execs))
	}
	// 2 条 fakeModel{Uid:"u",Name:"n"} × 2 字段 = 4 个参数
	if len(execs[0]) != 4 {
		t.Fatalf("replay Exec 参数应为 4 个（2 条 × 2 字段），实际 %d: %v", len(execs[0]), execs[0])
	}
	for i, v := range execs[0] {
		s, ok := v.(string)
		if !ok || (s != "u" && s != "n") {
			t.Errorf("replay Exec 参数[%d] 应为原始值 u/n，实际 %T(%v)", i, v, v)
		}
	}
	if got := q.Len(); got != 0 {
		t.Errorf("replay 成功后队列应清空，实际 %d", got)
	}
}

// ---- ReplayPayload / 未注册表 ----

func TestReplayPayload_UnknownTableReturnsError(t *testing.T) {
	p, err := encodeFailurePayload("e4_no_such_table", []any{"x", "y"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	rerr := ReplayPayload(p)
	if rerr == nil {
		t.Fatalf("未注册表的 payload 应返回错误")
	}
	if !errors.Is(rerr, errNoReplayFlusher) {
		t.Errorf("应可用 errors.Is 识别为 errNoReplayFlusher，实际 %v", rerr)
	}
}

func TestReplayQueue_RequeuesUnknownTableWithoutError(t *testing.T) {
	swapDefaultFailureQueueForTest(t, filepath.Join(t.TempDir(), "unknown.log"))
	q := defaultFailureQueue()
	p, _ := encodeFailurePayload("e4_unregistered_tbl", []any{"x", "y"}, 1)
	if err := q.Enqueue(p); err != nil {
		t.Fatal(err)
	}

	replayed, requeued, err := replayQueueForKnownTables(q)
	if err != nil {
		t.Fatalf("未注册表不算失败（等管理器创建再重放），不应返回 error: %v", err)
	}
	if replayed != 0 || requeued != 1 {
		t.Errorf("期望 replayed=0 requeued=1，实际 %d/%d", replayed, requeued)
	}
	if q.Len() != 1 {
		t.Errorf("未注册表的 payload 应留在队列，实际 Len=%d", q.Len())
	}
}

func TestReplayQueue_FlushFailureRequeuesAndReportsError(t *testing.T) {
	swapDefaultFailureQueueForTest(t, filepath.Join(t.TempDir(), "fail.log"))
	q := defaultFailureQueue()
	p, _ := encodeFailurePayload("e4_failing_tbl", []any{"x", "y"}, 1)
	_ = q.Enqueue(p)

	registerReplayFlusher("e4_failing_tbl", func(values []any, count int) error {
		return errors.New("db still down")
	})

	replayed, requeued, err := replayQueueForKnownTables(q)
	if err == nil {
		t.Fatalf("落库失败应聚合进返回错误")
	}
	if replayed != 0 || requeued != 1 {
		t.Errorf("期望 replayed=0 requeued=1，实际 %d/%d", replayed, requeued)
	}
	if q.Len() != 1 {
		t.Errorf("落库失败的 payload 应重新入队，实际 Len=%d", q.Len())
	}
}

// ---- builder.New()（InitStruct）真实接线验证 ----

type e4WireUser struct {
	Model
	Uid  string `orm:"pk"`
	Name string
}

func (u *e4WireUser) GetTableName() string { return "e4_wire_user" }

func TestInitStruct_WiresReplayFlusherAndRetry(t *testing.T) {
	swapDefaultFailureQueueForTest(t, filepath.Join(t.TempDir(), "wire.log"))

	b := NewAutoCacheBuilder[string, *e4WireUser]().
		WithLongevityCache(time.Second).
		WithLongevityRetry(7)
	svc := b.New()

	// E4：InitStruct 应按表名注册 replay flusher（启动 replay 的分发依据）
	if lookupReplayFlusher("e4_wire_user") == nil {
		t.Fatalf("InitStruct 应注册表 e4_wire_user 的 replay flusher")
	}
	// E6：WithLongevityRetry 应透传到 sqlBuilder
	m, ok := svc.(*autoCacheManager[string, *e4WireUser])
	if !ok {
		t.Fatalf("期望 *autoCacheManager，实际 %T", svc)
	}
	if m.sb.retryAttempts != 7 {
		t.Fatalf("WithLongevityRetry(7) 未透传到 flushBatch，实际 %d", m.sb.retryAttempts)
	}
}
