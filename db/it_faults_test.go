//go:build integration
// +build integration

package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description E5 · 故障注入：Redis 断 / MySQL 断（真实容器 stop/start）
//
// 路线图 §3.0-3"故障注入是一等公民"：断后框架不 panic、补偿/降级生效、
// 恢复后自愈——全部自动化（Consul 断见 rpc/internal/it_consul_test.go）。
//
// 容器 host 端口在创建时固定，stop/start 后保持不变，因此 go-redis 与
// database/sql 连接池能在依赖恢复后自动重连，"自愈"无需任何框架侧干预。
//
// 注意：本文件的两个用例会真实 stop/start 共享容器，用 defer 保证退出时
// 依赖已恢复，不影响同二进制内的后续用例。
//
//2026/6/10
//***************************************************

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/thkhxm/tgf"
)

// TestIT_Fault_MySQLDown_CompensateAndSelfHeal 全链路验证 MySQL 断电场景：
//   - 断前：write-behind 正常落库（基线）；
//   - 断中：flush 失败不 panic、脏标志保留、失败批次写入 File 补偿队列（降级/补偿生效）、
//     DB 直读返回 ErrDBFault 而非"无数据"（D5 语义，防止默认档覆盖老档）；
//   - 恢复：周期 flush 用保留的脏标志自动重试成功，数据零丢失（自愈）。
func TestIT_Fault_MySQLDown_CompensateAndSelfHeal(t *testing.T) {
	env := ensureIT(t)

	qPath := filepath.Join(t.TempDir(), "mysql_down.log")
	q, err := NewFileFailureQueue(qPath)
	if err != nil {
		t.Fatalf("创建 FileFailureQueue 失败: %v", err)
	}
	defer q.Close()

	m := NewAutoCacheBuilder[string, *itPlayer]().
		WithLongevityCache(time.Second).
		WithLongevityRetry(1). // 断中快速失败，缩短用例时长
		WithLongevityFailureQueue(q).
		New()

	// 基线：断前正常落库。
	m.Set(&itPlayer{Model: NewModel(), Id: "ft-base", Nickname: "alive", Level: 1}, "ft-base")
	waitUntil(t, 10*time.Second, "断前基线落库", func() bool {
		_, _, _, ok := env.queryPlayer("ft-base")
		return ok
	})

	// ---- 断 ----
	env.stopMySQL(t)
	mysqlRestored := false
	defer func() {
		if !mysqlRestored {
			env.startMySQL(t)
		}
	}()

	// 断中写入：进程必须不 panic，失败批次进补偿队列。
	m.Set(&itPlayer{Model: NewModel(), Id: "ft-down", Nickname: "queued", Level: 2}, "ft-down")
	waitUntil(t, 30*time.Second, "断中 flush 失败批次写入补偿队列", func() bool {
		return q.Len() >= 1
	})

	// 断中本地读不受影响（一级缓存命中）。
	if got, gerr := m.Get("ft-down"); gerr != nil || got.Nickname != "queued" {
		t.Fatalf("断中本地缓存读失败 err=%v got=%+v", gerr, got)
	}

	// 断中 DB 直读：必须是 ErrDBFault（真实故障），绝不能伪装成"无数据"。
	mNoCache := NewAutoCacheBuilder[string, *itPlayer]().
		WithLongevityCache(time.Minute).
		WithLongevityFailureQueue(NoopFailureQueue{}).
		New()
	if _, gerr := mNoCache.Get("ft-base"); gerr == nil {
		t.Fatal("断中 DB 直读不应成功")
	} else if !errors.Is(gerr, ErrDBFault) {
		t.Fatalf("断中 DB 直读应返回 ErrDBFault, got %v", gerr)
	} else if errors.Is(gerr, tgf.DBEmpty) {
		t.Fatalf("断中 DB 直读绝不能伪装成 DBEmpty: %v", gerr)
	}

	// ---- 恢复 ----
	env.startMySQL(t)
	mysqlRestored = true

	// 自愈：脏标志保留 → 周期 flush 自动重试 → 数据零丢失。
	waitUntil(t, 30*time.Second, "恢复后脏数据自动落库(自愈)", func() bool {
		nick, _, _, ok := env.queryPlayer("ft-down")
		return ok && nick == "queued"
	})

	// 恢复后 DB 直读自愈。
	if got, gerr := mNoCache.Get("ft-base"); gerr != nil || got.Nickname != "alive" {
		t.Fatalf("恢复后 DB 直读未自愈 err=%v got=%+v", gerr, got)
	}
}

// TestIT_Fault_RedisDown_DegradeAndSelfHeal 全链路验证 Redis 断电场景：
//   - 断中：所有缓存 API 不 panic；读 miss、写仅告警；显式错误 API（Incr/NewLock）
//     如实返回 error（登录锁降级为可观测失败，gate.Login 走拒绝分支而非 panic）；
//     带 DB 层的管理器读穿 Redis 后由 MySQL 兜底（降级生效）；
//   - 恢复：go-redis 自动重连，缓存读写与分布式锁全部自愈。
func TestIT_Fault_RedisDown_DegradeAndSelfHeal(t *testing.T) {
	env := ensureIT(t)

	// 基线：真实 Redis 读写。
	Set("it:fault:k1", "v1", time.Minute)
	if v, ok := Get[string]("it:fault:k1"); !ok || v != "v1" {
		t.Fatalf("断前基线 Redis 读写失败 ok=%v v=%v", ok, v)
	}

	// 先准备一条已落库的数据，供断中验证"MySQL 兜底"。
	mSeed := newITPlayerManager(true, "it:player:rdown")
	mSeed.Set(&itPlayer{Model: NewModel(), Id: "rd-1", Nickname: "db-backed", Level: 5}, "rd-1")
	waitUntil(t, 10*time.Second, "rd-1 落库", func() bool {
		_, _, _, ok := env.queryPlayer("rd-1")
		return ok
	})

	// ---- 断 ----
	env.stopRedis(t)
	redisRestored := false
	defer func() {
		if !redisRestored {
			env.startRedis(t)
		}
	}()

	// 断中：读 miss 不 panic。
	if _, ok := Get[string]("it:fault:k1"); ok {
		t.Fatal("Redis 已断, Get 不应返回成功")
	}
	// 断中：写不 panic（内部 WARN 日志）。
	Set("it:fault:k2", "v2", time.Minute)
	Del("it:fault:k2")
	DelNow("it:fault:k2")
	PutMap("it:fault:h", "f", "v", time.Minute)
	// 断中：显式错误 API 如实报错。
	if _, ierr := Incr("it:fault:cnt", time.Minute); ierr == nil {
		t.Fatal("Redis 已断, Incr 应返回 error")
	}
	// 断中：分布式登录锁降级为显式失败（gate.Login 依赖该语义走拒绝分支）。
	if _, lerr := NewLock("tgf:gate:login:lock:it-fault-user"); lerr == nil {
		t.Fatal("Redis 已断, NewLock 应返回 error")
	}

	// 断中：带 DB 层的全新管理器——Redis miss 后由 MySQL 兜底（降级生效）。
	mDuring := newITPlayerManager(true, "it:player:rdown")
	if got, gerr := mDuring.Get("rd-1"); gerr != nil || got.Nickname != "db-backed" {
		t.Fatalf("Redis 断中 MySQL 兜底失败 err=%v got=%+v", gerr, got)
	}

	// ---- 恢复 ----
	env.startRedis(t)
	redisRestored = true

	// 自愈：读写与锁全部恢复（go-redis 对同地址自动重连）。
	waitUntil(t, 15*time.Second, "Redis 恢复后读写自愈", func() bool {
		Set("it:fault:heal", "ok", time.Minute)
		v, ok := Get[string]("it:fault:heal")
		return ok && v == "ok"
	})
	lock, lerr := NewLock("tgf:gate:login:lock:it-heal-user")
	if lerr != nil {
		t.Fatalf("Redis 恢复后 NewLock 未自愈: %v", lerr)
	}
	UnLock(lock)
}
