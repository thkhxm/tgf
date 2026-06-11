package db

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ群 7400585
// author tim.huang<thkhxm@gmail.com>
// @Description D5 故障路径白盒测试（不依赖真实 Redis/MySQL）：
//  1. Redis 启动失败不再产生 typed-nil cache 接口（setCacheService 防线）
//  2. Redis 故障时 Incr/IncrBy/LLen 如实返回 error（不再静默归零）
//  3. MySQL 不可用/Prepare/Query 失败时 queryOne/queryList/execBatchOnce
//     返回 error 而不是 panic（defer 顺序 + nil conn 修复回归）
//  4. MySQL 宕机时落库失败真正进入补偿队列（FailureQueue），脏标志保留
//  5. Get/GetAll 区分"DB 故障"（ErrDBFault）与"无数据"（tgf.DBEmpty）
//  6. FlushNow/FlushAll 终末 flush 钩子：阻塞可靠、返回结构化结果
//
// 2026/6/10
// ***************************************************

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cornelk/hashmap"
	"github.com/redis/go-redis/v9"
	"github.com/thkhxm/tgf/v2"
	"golang.org/x/sync/singleflight"
)

// ---------------------------------------------------------------------------
// 假 SQL driver：按 DSN 选择故障形态，覆盖 Prepare 失败 / Query 失败 /
// Begin 失败 / 空结果集 四类路径，全程不需要真实 MySQL。
// ---------------------------------------------------------------------------

type fakeDriver struct{}

func (fakeDriver) Open(dsn string) (driver.Conn, error) {
	switch dsn {
	case "prepare-fail":
		return &fakeConn{prepareErr: errors.New("prepare boom")}, nil
	case "query-fail":
		return &fakeConn{queryErr: errors.New("query boom")}, nil
	case "begin-fail":
		return &fakeConn{beginErr: errors.New("begin boom")}, nil
	}
	// 默认：一切成功但结果集为空
	return &fakeConn{}, nil
}

type fakeConn struct {
	prepareErr error
	queryErr   error
	beginErr   error
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	if c.prepareErr != nil {
		return nil, c.prepareErr
	}
	return &fakeStmt{conn: c}, nil
}
func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	return fakeTx{}, nil
}

// BeginTx 让 database/sql 接受非默认隔离级别（execBatchOnce 用 ReadUncommitted）。
func (c *fakeConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	return fakeTx{}, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct{ conn *fakeConn }

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.conn.queryErr != nil {
		return nil, s.conn.queryErr
	}
	return &fakeRows{}, nil
}

type fakeRows struct{}

func (r *fakeRows) Columns() []string              { return []string{} }
func (r *fakeRows) Close() error                   { return nil }
func (r *fakeRows) Next(dest []driver.Value) error { return io.EOF }

func init() {
	sql.Register("tgf-d5-fake", fakeDriver{})
}

// withFakeDB 把包级 dbService 替换为假 driver 支撑的实例，测试结束自动还原。
func withFakeDB(t *testing.T, dsn string) {
	t.Helper()
	d, err := sql.Open("tgf-d5-fake", dsn)
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	old := dbService
	dbService = &mysqlService{running: true, db: d}
	t.Cleanup(func() {
		dbService = old
		_ = d.Close()
	})
}

// withDownMySQL 模拟 MySQL 完全不可用（initMySql 失败后的进程状态）。
func withDownMySQL(t *testing.T) {
	t.Helper()
	old := dbService
	dbService = &mysqlService{running: false}
	t.Cleanup(func() { dbService = old })
}

// newD5SqlBuilder 构造最小可用的 sqlBuilder，querySql/queryListSql 共用同一条语句。
func newD5SqlBuilder(querySql string) *sqlBuilder[*fakeModel] {
	return &sqlBuilder[*fakeModel]{
		tableName:      "fake",
		modelFieldName: []string{"Uid", "Name"},
		querySql:       querySql,
		queryListSql:   querySql,
	}
}

// newD5Manager 构造可走真实 queryOne 路径的 autoCacheManager
// （mem/cache 关闭，longevity 开启，直接命中 DB 分支）。
func newD5Manager(querySql string) *autoCacheManager[string, *fakeModel] {
	m := &autoCacheManager[string, *fakeModel]{
		cacheMap:      hashmap.New[string, *cacheData[*fakeModel]](),
		longevityLock: &sync.Mutex{},
		sf:            &singleflight.Group{},
		builder: &AutoCacheBuilder[string, *fakeModel]{
			longevity: true,
		},
		sb: newD5SqlBuilder(querySql),
	}
	m.flushBatch = m.sb.flushBatch
	return m
}

// ---------------------------------------------------------------------------
// 1. Redis typed-nil 防线
// ---------------------------------------------------------------------------

func TestSetCacheService_FailureKeepsCacheTrueNil(t *testing.T) {
	old := cache
	cache = nil
	t.Cleanup(func() { cache = old })

	// 初始化失败：cache 必须保持"真 nil"，所有 cache==nil 防御继续生效。
	// 旧实现 `cache = newRedisService()` 会塞进 typed-nil，使 cache != nil。
	setCacheService(nil, errors.New("ping failed"))
	if cache != nil {
		t.Fatalf("cache 应当保持真 nil，实际为 %T", cache)
	}

	// 失败初始化后，包级入口必须安全降级而不是 panic。
	if _, ok := Get[string]("d5:any"); ok {
		t.Errorf("cache 为 nil 时 Get 应该直接失败")
	}
	Set("d5:any", "v", time.Minute) // 不应 panic
	Del("d5:any")                   // 不应 panic
	if _, err := Incr("d5:any", 0); err != nil {
		// cache 为 nil 时 Incr 返回零值且无错误（与历史行为一致），仅确认不 panic
		t.Errorf("cache 为 nil 时 Incr 不应返回错误，got %v", err)
	}

	// 成功路径：cache 被正确装入。
	setCacheService(&redisService{}, nil)
	if cache == nil {
		t.Fatalf("初始化成功时 cache 应当被装入")
	}
}

// ---------------------------------------------------------------------------
// 2. Redis 故障时计数类操作如实返回 error
// ---------------------------------------------------------------------------

// newDeadRedisService 指向必然拒绝连接的地址（端口 1），不依赖真实 Redis。
func newDeadRedisService() *redisService {
	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:        []string{"127.0.0.1:1"},
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		MaxRetries:   -1, // 关闭重试，加速失败
	})
	return &redisService{client: client}
}

func TestRedisCounters_DownPropagateError(t *testing.T) {
	r := newDeadRedisService()
	defer func() { _ = r.client.Close() }()

	if _, err := r.Incr("d5:test:incr", 0); err == nil {
		t.Errorf("redis 故障时 Incr 应返回 error（旧实现恒返回 nil，计数被静默归零）")
	}
	if _, err := r.IncrBy("d5:test:incrby", 1.5, 0); err == nil {
		t.Errorf("redis 故障时 IncrBy 应返回 error")
	}
	if _, err := r.LLen("d5:test:llen"); err == nil {
		t.Errorf("redis 故障时 LLen 应返回 error")
	}
}

// ---------------------------------------------------------------------------
// 3. MySQL 故障：读写路径返回 error 而不是 panic
// ---------------------------------------------------------------------------

func TestQueryOne_MySQLDownReturnsErrorNotPanic(t *testing.T) {
	withDownMySQL(t)
	sb := newD5SqlBuilder("select `uid`,`name` from fake where uid = ?")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MySQL 不可用时 queryOne 不应 panic: %v", r)
		}
	}()
	_, err := sb.queryOne("u1")
	if !errors.Is(err, ErrMySQLNotAvailable) {
		t.Fatalf("期望 ErrMySQLNotAvailable，got %v", err)
	}
}

func TestQueryOne_PrepareFailReturnsErrorNotPanic(t *testing.T) {
	withFakeDB(t, "prepare-fail")
	sb := newD5SqlBuilder("select `uid`,`name` from fake where uid = ?")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Prepare 失败时 queryOne 不应 panic（defer 顺序回归）: %v", r)
		}
	}()
	_, err := sb.queryOne("u1")
	if err == nil || !strings.Contains(err.Error(), "prepare boom") {
		t.Fatalf("期望 prepare boom 错误，got %v", err)
	}
}

func TestQueryOne_QueryFailReturnsErrorNotPanic(t *testing.T) {
	withFakeDB(t, "query-fail")
	sb := newD5SqlBuilder("select `uid`,`name` from fake where uid = ?")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Query 失败时 queryOne 不应 panic（rows defer 顺序回归）: %v", r)
		}
	}()
	_, err := sb.queryOne("u1")
	if err == nil || !strings.Contains(err.Error(), "query boom") {
		t.Fatalf("期望 query boom 错误，got %v", err)
	}
}

func TestQueryOne_EmptyRowsReturnsDBEmpty(t *testing.T) {
	withFakeDB(t, "empty-rows")
	sb := newD5SqlBuilder("select `uid`,`name` from fake where uid = ?")

	_, err := sb.queryOne("u1")
	if !errors.Is(err, tgf.DBEmpty) {
		t.Fatalf("查无此行应返回 tgf.DBEmpty，got %v", err)
	}
}

func TestQueryOne_EmptyScriptReturnsError(t *testing.T) {
	sb := &sqlBuilder[*fakeModel]{}
	_, err := sb.queryOne("u1")
	if err == nil {
		t.Fatalf("querySql 为空时应返回 error 而不是 (零值, nil)")
	}
}

func TestQueryList_MySQLDownReturnsErrorNotPanic(t *testing.T) {
	withDownMySQL(t)
	sb := newD5SqlBuilder("select `uid`,`name` from fake where uid = ?")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MySQL 不可用时 queryList 不应 panic: %v", r)
		}
	}()
	_, err := sb.queryList("u1")
	if !errors.Is(err, ErrMySQLNotAvailable) {
		t.Fatalf("期望 ErrMySQLNotAvailable，got %v", err)
	}
}

func TestExecBatchOnce_MySQLDownReturnsErrorNotPanic(t *testing.T) {
	withDownMySQL(t)
	sb := newD5SqlBuilder("")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MySQL 不可用时 execBatchOnce 不应 panic: %v", r)
		}
	}()
	err := sb.execBatchOnce("INSERT INTO fake VALUES (?,?)", []any{"u", "n"})
	if !errors.Is(err, ErrMySQLNotAvailable) {
		t.Fatalf("期望 ErrMySQLNotAvailable，got %v", err)
	}
}

func TestExecBatchOnce_BeginFailReturnsError(t *testing.T) {
	withFakeDB(t, "begin-fail")
	sb := newD5SqlBuilder("")

	err := sb.execBatchOnce("INSERT INTO fake VALUES (?,?)", []any{"u", "n"})
	if err == nil || !strings.Contains(err.Error(), "begin boom") {
		t.Fatalf("期望 begin boom 错误，got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 4. MySQL 宕机 → 真实 flushBatch 路径 → 补偿队列接管（不再被 panic 旁路）
// ---------------------------------------------------------------------------

func TestToLongevity_MySQLDownEnqueuesCompensationQueue(t *testing.T) {
	withDownMySQL(t)
	m := newTestManager(2) // groupSize=2 → 3 条切成 2 批 (2,1)
	items := seedDirty(m, 3)
	q := NewMemoryFailureQueue()
	m.builder.longevityFailureQueue = q
	// 关键：用真实 flushBatch（flushBatch → execBatchOnce → getMysqlConn）。
	// 旧实现 nil conn panic 会穿透 batch 循环、旁路掉补偿队列。
	m.flushBatch = m.sb.flushBatch

	m.toLongevity()

	if got := q.Len(); got != 2 {
		t.Errorf("MySQL 宕机时每个失败 batch 都应进补偿队列，期望 2 条 payload，got %d", got)
	}
	for i, cd := range items {
		if !cd.checkState(data_update) {
			t.Errorf("item %d 的脏标志丢失——落库失败时必须保留，交给下一轮重试", i)
		}
	}
	// payload 可被解码且表名正确（replay 侧契约）
	payloads, err := q.Drain()
	if err != nil {
		t.Fatalf("Drain 失败: %v", err)
	}
	for _, p := range payloads {
		doc, derr := DecodeFailurePayload(p)
		if derr != nil {
			t.Fatalf("payload 解码失败: %v", derr)
		}
		if doc.Table != "fake" {
			t.Errorf("payload 表名错误 got=%s", doc.Table)
		}
	}
}

// ---------------------------------------------------------------------------
// 5. Get/GetAll 区分"DB 故障"与"无数据"
// ---------------------------------------------------------------------------

func TestGet_DBFaultDistinguishedFromEmpty(t *testing.T) {
	withDownMySQL(t)
	m := newD5Manager("select `uid`,`name` from fake where uid = ?")

	_, err := m.Get("u1")
	if err == nil {
		t.Fatalf("DB 故障时 Get 应返回 error")
	}
	if !errors.Is(err, ErrDBFault) {
		t.Errorf("DB 故障应可用 errors.Is(err, ErrDBFault) 识别，got %v", err)
	}
	if !errors.Is(err, ErrMySQLNotAvailable) {
		t.Errorf("原始错误应被保留（%%w 链），got %v", err)
	}
	if errors.Is(err, tgf.DBEmpty) {
		t.Errorf("DB 故障绝不能伪装成 DBEmpty——这是老档被默认档覆盖的事故路径, got %v", err)
	}
}

func TestGet_NoRowsReturnsDBEmpty(t *testing.T) {
	withFakeDB(t, "empty-rows")
	m := newD5Manager("select `uid`,`name` from fake where uid = ?")

	_, err := m.Get("u1")
	if !errors.Is(err, tgf.DBEmpty) {
		t.Fatalf("查无此行应返回 tgf.DBEmpty（新玩家语义不变），got %v", err)
	}
	if errors.Is(err, ErrDBFault) {
		t.Errorf("真正的无数据不应带 ErrDBFault 标记, got %v", err)
	}
}

// ---- hash 管理器的同源路径 ----

type fakeHashModel struct {
	Uid  string
	Item string
}

func (f *fakeHashModel) HashCachePkKey(key ...string) string { return "hash:" + key[0] }
func (f *fakeHashModel) HashCacheFieldByVal() string         { return f.Item }
func (f *fakeHashModel) HashCacheFieldByKeys(key ...string) string {
	if len(key) > 1 {
		return key[1]
	}
	return ""
}

func newD5HashManager() *hashAutoCacheManager[*fakeHashModel] {
	h := &hashAutoCacheManager[*fakeHashModel]{}
	h.builder = &AutoCacheBuilder[string, *fakeHashModel]{longevity: true}
	h.cacheMap = hashmap.New[string, *cacheData[*fakeHashModel]]()
	h.longevityLock = &sync.Mutex{}
	h.sf = &singleflight.Group{}
	h.sb = &sqlBuilder[*fakeHashModel]{
		tableName:      "fake_hash",
		modelFieldName: []string{"Uid", "Item"},
		queryListSql:   "select `uid`,`item` from fake_hash where uid = ?",
	}
	h.flushBatch = h.sb.flushBatch
	h.image = &fakeHashModel{}
	h.groupAutoCacheManager = NewAutoCacheBuilder[string, []string]().WithMemCache(60).New()
	return h
}

func TestHashGet_DBFaultNotCachedAsLoaded(t *testing.T) {
	withDownMySQL(t)
	h := newD5HashManager()

	_, err := h.Get("u1", "sword")
	if !errors.Is(err, ErrDBFault) {
		t.Fatalf("hash Get 在 DB 故障时应返回 ErrDBFault，got %v", err)
	}
	// 故障期间不能把空 key 列表当"已加载"缓存——否则故障恢复后数据仍读不到
	if _, gerr := h.groupAutoCacheManager.Get("hash:u1"); gerr == nil {
		t.Errorf("DB 故障时不应缓存'已加载'状态（空 key 列表）")
	}
}

func TestHashGetAll_DBFaultNotDBEmpty(t *testing.T) {
	withDownMySQL(t)
	h := newD5HashManager()

	_, err := h.GetAll("u1")
	if !errors.Is(err, ErrDBFault) {
		t.Fatalf("hash GetAll 在 DB 故障时应返回 ErrDBFault，got %v", err)
	}
	if errors.Is(err, tgf.DBEmpty) {
		t.Errorf("DB 故障不能伪装成 DBEmpty, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 6. FlushNow / FlushAll 终末 flush 钩子
// ---------------------------------------------------------------------------

func TestFlushNow_SuccessClearsDirty(t *testing.T) {
	m := newTestManager(2)
	items := seedDirty(m, 5)

	var calls int
	m.flushBatch = func(values []any, count int) error {
		calls++
		return nil
	}

	res := m.FlushNow()
	if res.Err != nil {
		t.Fatalf("全部成功时 Err 应为 nil, got %v", res.Err)
	}
	if res.Success != 5 || res.Failed != 0 {
		t.Errorf("期望 Success=5 Failed=0, got Success=%d Failed=%d", res.Success, res.Failed)
	}
	if res.Table != "fake" {
		t.Errorf("Table 应为 fake, got %s", res.Table)
	}
	if calls != 3 { // 5 条 groupSize=2 → 3 批
		t.Errorf("期望 3 批, got %d", calls)
	}
	for i, cd := range items {
		if cd.checkState(data_update) {
			t.Errorf("item %d 的脏标志在成功 flush 后应被清除", i)
		}
	}
}

func TestFlushNow_FailureKeepsDirtyAndEnqueues(t *testing.T) {
	m := newTestManager(2)
	items := seedDirty(m, 3)
	q := NewMemoryFailureQueue()
	m.builder.longevityFailureQueue = q
	m.flushBatch = func(values []any, count int) error {
		return errors.New("mysql down")
	}

	res := m.FlushNow()
	if res.Err == nil {
		t.Fatalf("落库失败时 Err 不应为 nil")
	}
	if res.Failed != 3 || res.Success != 0 {
		t.Errorf("期望 Success=0 Failed=3, got Success=%d Failed=%d", res.Success, res.Failed)
	}
	if got := q.Len(); got != 2 { // 3 条 groupSize=2 → 2 批
		t.Errorf("失败 batch 应进补偿队列，期望 2 条 payload，got %d", got)
	}
	for i, cd := range items {
		if !cd.checkState(data_update) {
			t.Errorf("item %d 的脏标志丢失——flush 失败必须保留", i)
		}
	}
}

func TestFlushNow_NotLongevityIsNoop(t *testing.T) {
	m := newTestManager(2)
	m.builder.longevity = false
	seedDirty(m, 2)

	var calls int
	m.flushBatch = func(values []any, count int) error {
		calls++
		return nil
	}

	res := m.FlushNow()
	if res.Err != nil || res.Success != 0 || res.Failed != 0 || calls != 0 {
		t.Errorf("非 longevity 管理器 FlushNow 应为空操作, got %+v calls=%d", res, calls)
	}
}

// FlushNow 必须阻塞等待正在进行的周期 flush 完成（非 TryLock 尽力而为）。
func TestFlushNow_BlocksUntilPeriodicFlushDone(t *testing.T) {
	m := newTestManager(2)
	seedDirty(m, 2)
	m.flushBatch = func(values []any, count int) error { return nil }

	// 模拟周期 flush 正在执行：先持锁
	m.longevityLock.Lock()

	done := make(chan FlushResult, 1)
	go func() {
		done <- m.FlushNow()
	}()

	select {
	case <-done:
		t.Fatalf("FlushNow 在锁被占用时不应提前返回（旧 Destroy 的 TryLock 行为是直接放弃）")
	case <-time.After(150 * time.Millisecond):
		// 预期：仍在阻塞等锁
	}

	m.longevityLock.Unlock()

	select {
	case res := <-done:
		if res.Err != nil || res.Success != 2 {
			t.Errorf("锁释放后 FlushNow 应完成全部落库, got %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("锁释放后 FlushNow 应在合理时间内完成")
	}
}

func TestFlushAll_CollectsAllRegisteredManagers(t *testing.T) {
	// 隔离全局注册表，避免污染其它测试
	flushRegistryMu.Lock()
	old := flushRegistry
	flushRegistry = nil
	flushRegistryMu.Unlock()
	t.Cleanup(func() {
		flushRegistryMu.Lock()
		flushRegistry = old
		flushRegistryMu.Unlock()
	})

	m1 := newTestManager(2)
	seedDirty(m1, 3)
	m1.flushBatch = func(values []any, count int) error { return nil }

	m2 := newTestManager(2)
	m2.sb.tableName = "fake2"
	seedDirty(m2, 2)
	q := NewMemoryFailureQueue()
	m2.builder.longevityFailureQueue = q
	m2.flushBatch = func(values []any, count int) error { return errors.New("boom") }

	registerFlushable(m1)
	registerFlushable(m2)

	results := FlushAll()
	if len(results) != 2 {
		t.Fatalf("期望 2 个结果, got %d", len(results))
	}
	byTable := map[string]FlushResult{}
	for _, r := range results {
		byTable[r.Table] = r
	}
	if r := byTable["fake"]; r.Err != nil || r.Success != 3 || r.Failed != 0 {
		t.Errorf("fake 表应全部成功, got %+v", r)
	}
	if r := byTable["fake2"]; r.Err == nil || r.Failed != 2 || r.Success != 0 {
		t.Errorf("fake2 表应全部失败且带错误, got %+v", r)
	}
	if q.Len() != 1 { // 2 条 groupSize=2 → 1 批
		t.Errorf("fake2 的失败 batch 应进补偿队列, got %d", q.Len())
	}
	// 失败 manager 不应中断其它 manager 的 flush——上面 len(results)==2 已隐式验证
}

// Destroy 改为阻塞可靠 flush 的回归：脏数据在 Destroy 后必须已尝试落库。
func TestDestroy_FlushesDirtyReliably(t *testing.T) {
	m := newTestManager(2)
	items := seedDirty(m, 3)
	var calls int
	m.flushBatch = func(values []any, count int) error {
		calls++
		return nil
	}

	m.Destroy()

	if calls == 0 {
		t.Fatalf("Destroy 应触发终末 flush")
	}
	for i, cd := range items {
		if cd.checkState(data_update) {
			t.Errorf("item %d 在 Destroy 后仍有脏标志", i)
		}
	}
}
