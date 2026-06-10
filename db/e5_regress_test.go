package db

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ群 7400585
// @Description E5 回归单测（默认 tag，无外部依赖）。
//
// E5 的真实容器集成测试暴露了两个生产 bug，本文件用白盒单测把修复锁进
// CI 单元轨（集成轨的端到端验证见 it_writebehind_test.go / manager_test.go）：
//  1. initField 的 state 列识别：ConvertCamelToSnake 返回带反引号的 "`state`"，
//     旧实现与裸名 StateName("state") 比较永远为假 → hasState 从未置位 →
//     querySql/queryListSql 缺 "and state = 1" → 已删除(state=0)的行重载复活。
//  2. hashAutoCacheManager.loadCache 把全部 key（pk键+field键）透传给只含
//     pkList 占位符的 queryList → 真实驱动校验占位符个数，冷缓存下复合 key 的
//     Get 必报 "sql: expected N arguments, got M"。
//
// 2026/6/10
// ***************************************************

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 记录型 fake driver：NumInput 取自 DSN（模拟真实驱动的占位符个数校验），
// 并记录 Query 收到的参数。
// ---------------------------------------------------------------------------

var (
	e5ArgsMu       sync.Mutex
	e5LastQueryArg []driver.Value
)

func e5TakeLastQueryArgs() []driver.Value {
	e5ArgsMu.Lock()
	defer e5ArgsMu.Unlock()
	out := e5LastQueryArg
	e5LastQueryArg = nil
	return out
}

type e5Driver struct{}

func (e5Driver) Open(dsn string) (driver.Conn, error) {
	n, err := strconv.Atoi(dsn)
	if err != nil {
		n = -1
	}
	return &e5Conn{numInput: n}, nil
}

type e5Conn struct{ numInput int }

func (c *e5Conn) Prepare(query string) (driver.Stmt, error) {
	return &e5Stmt{numInput: c.numInput}, nil
}
func (c *e5Conn) Close() error              { return nil }
func (c *e5Conn) Begin() (driver.Tx, error) { return e5Tx{}, nil }

type e5Tx struct{}

func (e5Tx) Commit() error   { return nil }
func (e5Tx) Rollback() error { return nil }

type e5Stmt struct{ numInput int }

func (s *e5Stmt) Close() error  { return nil }
func (s *e5Stmt) NumInput() int { return s.numInput }
func (s *e5Stmt) Exec(args []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (s *e5Stmt) Query(args []driver.Value) (driver.Rows, error) {
	e5ArgsMu.Lock()
	e5LastQueryArg = append([]driver.Value(nil), args...)
	e5ArgsMu.Unlock()
	return &e5Rows{}, nil
}

type e5Rows struct{}

func (r *e5Rows) Columns() []string              { return []string{} }
func (r *e5Rows) Close() error                   { return nil }
func (r *e5Rows) Next(dest []driver.Value) error { return io.EOF }

func init() {
	sql.Register("tgf-e5-rec", e5Driver{})
}

// withE5DB 把包级 dbService 换成"占位符个数 = numInput"的记录型 fake driver。
func withE5DB(t *testing.T, numInput int) {
	t.Helper()
	d, err := sql.Open("tgf-e5-rec", strconv.Itoa(numInput))
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

// ---------------------------------------------------------------------------
// 测试模型
// ---------------------------------------------------------------------------

type e5Player struct {
	Model
	Id   string `orm:"pk"`
	Name string
}

func (p *e5Player) GetTableName() string { return "e5_player" }

type e5NoState struct {
	Id   string `orm:"pk"`
	Name string
}

func (p *e5NoState) GetTableName() string { return "e5_nostate" }

type e5HashItem struct {
	Model
	Uid  string `orm:"pk;pkList"`
	Prop string `orm:"pk"`
	Num  uint64
}

func (i *e5HashItem) GetTableName() string                      { return "e5_hash_item" }
func (i *e5HashItem) HashCachePkKey(key ...string) string       { return key[0] }
func (i *e5HashItem) HashCacheFieldByVal() string               { return i.Prop }
func (i *e5HashItem) HashCacheFieldByKeys(key ...string) string { return key[1] }

// ---------------------------------------------------------------------------
// 回归 1：state 列识别与删除过滤
// ---------------------------------------------------------------------------

func TestE5_InitField_StateFilterApplied(t *testing.T) {
	t.Run("内嵌Model的类型必须识别state并追加过滤", func(t *testing.T) {
		b := NewAutoCacheBuilder[string, *e5Player]()
		b.WithLongevityCache(time.Minute)
		m := b.New().(*autoCacheManager[string, *e5Player])

		if !m.sb.hasState {
			t.Fatal("hasState 应为 true（State 列经 ConvertCamelToSnake 后带反引号，比较必须去引号）")
		}
		if !strings.Contains(m.sb.querySql, "state = 1") {
			t.Fatalf("querySql 缺少删除过滤: %s", m.sb.querySql)
		}
		if !strings.Contains(m.sb.queryListSql, "state = 1") {
			t.Fatalf("queryListSql 缺少删除过滤: %s", m.sb.queryListSql)
		}
	})

	t.Run("无State列的类型不追加过滤", func(t *testing.T) {
		b := NewAutoCacheBuilder[string, *e5NoState]()
		b.WithLongevityCache(time.Minute)
		m := b.New().(*autoCacheManager[string, *e5NoState])

		if m.sb.hasState {
			t.Fatal("无 State 列时 hasState 应为 false")
		}
		if strings.Contains(m.sb.querySql, "state = 1") {
			t.Fatalf("querySql 不应有 state 过滤: %s", m.sb.querySql)
		}
	})
}

// ---------------------------------------------------------------------------
// 回归 2：hash 冷缓存加载只传 pkList 主键参数
// ---------------------------------------------------------------------------

func TestE5_HashLoadCache_OnlyPKListArgs(t *testing.T) {
	// queryListSql 只有 1 个占位符（uid）；NumInput=1 模拟真实驱动的个数校验。
	tests := []struct {
		name     string
		call     func(h IHashCacheService[*e5HashItem]) error
		wantArgs []driver.Value
	}{
		{
			name: "复合key的Get只传pkList前缀",
			call: func(h IHashCacheService[*e5HashItem]) error {
				_, err := h.Get("u1", "p1")
				return err
			},
			wantArgs: []driver.Value{"u1"},
		},
		{
			name: "GetAll本就只传pkList键",
			call: func(h IHashCacheService[*e5HashItem]) error {
				_, err := h.GetAll("u2")
				return err
			},
			wantArgs: []driver.Value{"u2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withE5DB(t, 1)
			e5TakeLastQueryArgs() // 清残留
			h := NewHashAutoCacheBuilder[*e5HashItem]().
				WithLongevityCache(time.Minute).
				WithMemCache(300).
				New()

			err := tt.call(h)
			// 空结果集下允许 "data not found" / DBEmpty 之类的业务错误，
			// 但绝不能是 DB 故障（修复前是 "sql: expected 1 arguments, got 2" → ErrDBFault）。
			if err != nil && errors.Is(err, ErrDBFault) {
				t.Fatalf("冷缓存加载不应触发 DB 故障（参数个数错配回归）: %v", err)
			}
			got := e5TakeLastQueryArgs()
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("queryList 收到参数 %v, want %v", got, tt.wantArgs)
			}
			for i := range got {
				if got[i] != tt.wantArgs[i] {
					t.Fatalf("queryList 参数[%d] = %v, want %v", i, got[i], tt.wantArgs[i])
				}
			}
		})
	}
}
