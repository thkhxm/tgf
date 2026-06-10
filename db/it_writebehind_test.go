//go:build integration
// +build integration

package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description E5 · write-behind 真实落库 / 真实回读 / 删除不复活（真 Redis + 真 MySQL）
//
// 审计指出旧测试体系"所有真实 Redis/MySQL 路径被 integration tag 隔离且自身带病，
// 从未真正跑过"。本文件以真实容器验证核心数据链路的端到端行为：
//   1. Set/Push → 周期 flush → MySQL 真实出现该行（write-behind 落库）；
//   2. Redis 二级缓存真实写入，且 DB 回读后回填 Redis；
//   3. Remove → tombstone(state=0) 真实写进 DB，重载不复活（E4 修复的端到端证明）；
//   4. FlushNow/FlushAll 停机钩子把脏数据同步落库（D3 契约）。
//
//2026/6/10
//***************************************************

import (
	"fmt"
	"testing"
	"time"
)

// newITPlayerManager 构造指向 it_player 表的 longevity 管理器。
// flushInterval 受 WithLongevityCache 的 1s 下限约束。
func newITPlayerManager(withRedis bool, keyFun string) IAutoCacheService[string, *itPlayer] {
	b := NewAutoCacheBuilder[string, *itPlayer]().
		WithLongevityCache(time.Second).
		WithLongevityFailureQueue(NoopFailureQueue{})
	if withRedis {
		b.WithAutoCache(keyFun, time.Minute)
	}
	return b.New()
}

func TestIT_WriteBehind_RealFlushToMySQL(t *testing.T) {
	env := ensureIT(t)

	m := newITPlayerManager(true, "it:player:wb")
	p := &itPlayer{Model: NewModel(), Id: "wb-1", Nickname: "tim", Level: 7}
	if ok := m.Set(p, "wb-1"); !ok {
		t.Fatal("Set 失败")
	}

	// 周期 flush（1s）后 MySQL 必须真实出现该行。
	waitUntil(t, 10*time.Second, "wb-1 落库", func() bool {
		nick, level, state, ok := env.queryPlayer("wb-1")
		return ok && nick == "tim" && level == 7 && state == 1
	})

	// Redis 二级缓存真实写入（生产序列化路径：sonic JSON）。
	if got, ok := Get[*itPlayer]("it:player:wb:wb-1"); !ok || got == nil || got.Nickname != "tim" {
		t.Fatalf("Redis 二级缓存未命中或数据不符 ok=%v got=%+v", ok, got)
	}

	// 原地修改 + Push → 增量更新真实落库（upsert 幂等路径）。
	cur, err := m.Get("wb-1")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	cur.Level = 99
	m.Push("wb-1")
	waitUntil(t, 10*time.Second, "wb-1 增量更新落库 level=99", func() bool {
		_, level, _, ok := env.queryPlayer("wb-1")
		return ok && level == 99
	})
}

func TestIT_WriteBehind_ReloadFromMySQLAndBackfillRedis(t *testing.T) {
	env := ensureIT(t)

	// 先用管理器 A 写入并等待落库。
	mA := newITPlayerManager(true, "it:player:reload")
	mA.Set(&itPlayer{Model: NewModel(), Id: "rl-1", Nickname: "from-db", Level: 11}, "rl-1")
	waitUntil(t, 10*time.Second, "rl-1 落库", func() bool {
		_, _, _, ok := env.queryPlayer("rl-1")
		return ok
	})

	// 无 Redis 层的全新管理器：本地缓存为空 → 必走 queryOne 真实回读 MySQL。
	mB := newITPlayerManager(false, "")
	got, err := mB.Get("rl-1")
	if err != nil {
		t.Fatalf("MySQL 真实回读失败: %v", err)
	}
	if got.Nickname != "from-db" || got.Level != 11 {
		t.Fatalf("回读数据不符: %+v", got)
	}

	// 清掉 Redis key 后，带 Redis 层的全新管理器走 DB 回读并回填 Redis。
	DelNow("it:player:reload:rl-1")
	mC := newITPlayerManager(true, "it:player:reload")
	if _, err = mC.Get("rl-1"); err != nil {
		t.Fatalf("DB 回读(带回填)失败: %v", err)
	}
	waitUntil(t, 5*time.Second, "Redis 回填", func() bool {
		_, ok := Get[*itPlayer]("it:player:reload:rl-1")
		return ok
	})
}

func TestIT_Remove_TombstoneWrittenAndNoResurrect(t *testing.T) {
	env := ensureIT(t)

	m := newITPlayerManager(true, "it:player:rm")
	m.Set(&itPlayer{Model: NewModel(), Id: "rm-1", Nickname: "doomed", Level: 1}, "rm-1")
	waitUntil(t, 10*time.Second, "rm-1 落库(state=1)", func() bool {
		_, _, state, ok := env.queryPlayer("rm-1")
		return ok && state == 1
	})

	// Remove：本地 tombstone + Redis 过期 + 删除态(state=0)随下一轮 flush 写进 DB。
	if ok := m.Remove("rm-1"); !ok {
		t.Fatal("Remove 失败")
	}
	waitUntil(t, 10*time.Second, "rm-1 tombstone 落库(state=0)", func() bool {
		_, _, state, ok := env.queryPlayer("rm-1")
		return ok && state == 0
	})

	// 确保 Redis 残留不会让数据从缓存复活（Del 是 1s 过期，直接清掉确定性更强）。
	DelNow("it:player:rm:rm-1")

	// 全新管理器重载：已删除数据绝不能复活（querySql 必须过滤 state=1，E4 端到端验收）。
	mB := newITPlayerManager(false, "")
	if got, err := mB.Get("rm-1"); err == nil {
		t.Fatalf("已删除数据从 DB 复活: %+v（querySql 未过滤 state=1）", got)
	}
}

func TestIT_FlushAll_ShutdownHookFlushesDirtyNow(t *testing.T) {
	env := ensureIT(t)

	// flush 间隔拉长到 60s——周期 flush 在测试窗口内不可能触发，
	// 行落库只能由 FlushAll（D3 停机钩子）驱动，证明钩子真实生效。
	m := NewAutoCacheBuilder[string, *itPlayer]().
		WithLongevityCache(time.Minute).
		WithLongevityFailureQueue(NoopFailureQueue{}).
		New()
	m.Set(&itPlayer{Model: NewModel(), Id: "fa-1", Nickname: "flush-all", Level: 3}, "fa-1")

	if _, _, _, ok := env.queryPlayer("fa-1"); ok {
		t.Fatal("前置失败: 行不应在 FlushAll 之前出现")
	}

	results := FlushAll()
	var hit *FlushResult
	for i := range results {
		if results[i].Table == "it_player" && results[i].Success > 0 {
			hit = &results[i]
		}
		if results[i].Err != nil && results[i].Table == "it_player" {
			t.Fatalf("FlushAll it_player 出错: %v", results[i].Err)
		}
	}
	if hit == nil {
		t.Fatalf("FlushAll 未报告 it_player 的成功落库: %+v", results)
	}
	if nick, level, _, ok := env.queryPlayer("fa-1"); !ok || nick != "flush-all" || level != 3 {
		t.Fatalf("FlushAll 后行未真实落库 ok=%v nick=%v level=%v", ok, nick, level)
	}
}

func TestIT_HashManager_RealRoundTripAndRemove(t *testing.T) {
	env := ensureIT(t)

	newHash := func() IHashCacheService[*itItem] {
		return NewHashAutoCacheBuilder[*itItem]().
			WithLongevityCache(time.Second).
			WithMemCache(300).
			New()
	}

	h1 := newHash()
	for i := 1; i <= 2; i++ {
		prop := fmt.Sprintf("p%d", i)
		if ok := h1.Set(&itItem{Model: NewModel(), UserId: "u1", PropId: prop, Amount: uint64(i * 10)}, "u1", prop); !ok {
			t.Fatalf("hash Set %s 失败", prop)
		}
	}
	waitUntil(t, 10*time.Second, "u1 两个道具落库", func() bool {
		a1, s1, ok1 := env.queryItemState("u1", "p1")
		a2, s2, ok2 := env.queryItemState("u1", "p2")
		return ok1 && ok2 && a1 == 10 && a2 == 20 && s1 == 1 && s2 == 1
	})

	// 全新 hash 管理器：本地为空 → queryList 真实回读 MySQL。
	h2 := newHash()
	all, err := h2.GetAll("u1")
	if err != nil {
		t.Fatalf("GetAll 回读失败: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("GetAll 期望 2 条, got %d: %+v", len(all), all)
	}
	got, err := h2.Get("u1", "p2")
	if err != nil || got.Amount != 20 {
		t.Fatalf("Get(u1,p2) 失败 err=%v got=%+v", err, got)
	}

	// Remove p2 → state=0 落库；再开新管理器重载只剩 p1（删除不复活，E4 hash 路径）。
	if ok := h2.Remove("u1", "p2"); !ok {
		t.Fatal("hash Remove 失败")
	}
	waitUntil(t, 10*time.Second, "p2 tombstone 落库(state=0)", func() bool {
		_, state, ok := env.queryItemState("u1", "p2")
		return ok && state == 0
	})

	h3 := newHash()
	all3, err := h3.GetAll("u1")
	if err != nil {
		t.Fatalf("GetAll(重载) 失败: %v", err)
	}
	if len(all3) != 1 || all3[0].PropId != "p1" {
		t.Fatalf("已删除的 p2 从 DB 复活: %+v（queryListSql 未过滤 state=1）", all3)
	}
}
