package db

// E6 · WithLongevityRetry 死配置接线回归：
// 用计数型 fake driver 统计 flushBatch 的真实重试次数（BeginTx 调用数），
// 证明 builder 配置端到端生效，而不是停在字段赋值。
// （Option → InitStruct → sqlBuilder 的透传见 e4_replay_test.go
// TestInitStruct_WiresReplayFlusherAndRetry。）

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
)

var e6BeginCount atomic.Int64

type e6CountingFailDriver struct{}

func (e6CountingFailDriver) Open(_ string) (driver.Conn, error) { return &e6FailConn{}, nil }

type e6FailConn struct{}

func (c *e6FailConn) Prepare(_ string) (driver.Stmt, error) {
	return nil, errors.New("e6: prepare unreachable")
}
func (c *e6FailConn) Close() error { return nil }
func (c *e6FailConn) Begin() (driver.Tx, error) {
	e6BeginCount.Add(1)
	return nil, errors.New("e6 begin fail")
}
func (c *e6FailConn) BeginTx(_ context.Context, _ driver.TxOptions) (driver.Tx, error) {
	e6BeginCount.Add(1)
	return nil, errors.New("e6 begin fail")
}

func init() {
	sql.Register("tgf-e6-counting-fail", e6CountingFailDriver{})
}

func withCountingFailDB(t *testing.T) {
	t.Helper()
	d, err := sql.Open("tgf-e6-counting-fail", "fail")
	if err != nil {
		t.Fatalf("open counting db: %v", err)
	}
	old := dbService
	dbService = &mysqlService{running: true, db: d}
	t.Cleanup(func() {
		dbService = old
		_ = d.Close()
	})
}

func TestFlushBatch_RetryAttemptsFromBuilderConfig(t *testing.T) {
	cases := []struct {
		name     string
		attempts int
		want     int64
	}{
		{"显式 1 次（不重试）", 1, 1},
		{"显式 2 次", 2, 2},
		{"未配置回退默认 3 次", 0, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCountingFailDB(t)
			e6BeginCount.Store(0)

			sb := &sqlBuilder[*fakeModel]{
				tableName:      "e6_retry_tbl",
				modelFieldName: []string{"Uid", "Name"},
				retryAttempts:  tc.attempts,
			}
			err := sb.flushBatch([]any{"u", "n"}, 1)
			if err == nil {
				t.Fatalf("全部尝试失败时 flushBatch 应返回 error")
			}
			if got := e6BeginCount.Load(); got != tc.want {
				t.Fatalf("WithLongevityRetry(%d) 实际重试次数应为 %d，观测到 %d 次 BeginTx"+
					"（旧 bug：字段从未被读取，永远是常量 3）", tc.attempts, tc.want, got)
			}
		})
	}
}
