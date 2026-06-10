//go:build integration
// +build integration

package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description E5 · 补偿队列启动 replay 的真实落库验证（真 MySQL）
//
// E4 把"FileFailureQueue + 启动 replay"接进了框架（wireFailureReplay），
// 既有单测用 recording driver 证明了 SQL 形状；本文件补上最后一环：
// 队列里的 payload 经启动 replay 后真实写进 MySQL（端到端，§3.0）。
//
//2026/6/10
//***************************************************

import (
	"path/filepath"
	"testing"
	"time"
)

// TestIT_FailureQueue_StartupReplayLandsInMySQL 模拟"上一个进程在 MySQL 宕机期间
// 把失败批次写进 File 补偿队列后崩溃；新进程创建同表管理器时启动 replay"：
//  1. 往 FileFailureQueue 预置一条 it_player 的 payload（与 flushDirtyLocked
//     的 encodeFailurePayload 完全同一编码路径）；
//  2. 创建指向同一张表的 longevity 管理器 → InitStruct → wireFailureReplay
//     启动重放；
//  3. 断言 payload 中的行真实出现在 MySQL，且队列被清空。
func TestIT_FailureQueue_StartupReplayLandsInMySQL(t *testing.T) {
	env := ensureIT(t)

	qPath := filepath.Join(t.TempDir(), "replay.log")
	q, err := NewFileFailureQueue(qPath)
	if err != nil {
		t.Fatalf("创建 FileFailureQueue 失败: %v", err)
	}
	defer q.Close()

	// 字段顺序必须与 sqlBuilder.modelFieldName 一致：State, Id, Nickname, Level
	// （embedded Model 在前，见 initField 的递归展开顺序）。
	values := []any{uint8(1), "rp-1", "from-queue", int64(42)}
	payload, err := encodeFailurePayload("it_player", values, 1)
	if err != nil {
		t.Fatalf("encodeFailurePayload 失败: %v", err)
	}
	if err = q.Enqueue(payload); err != nil {
		t.Fatalf("Enqueue 失败: %v", err)
	}
	if q.Len() != 1 {
		t.Fatalf("前置失败: 队列长度应为 1, got %d", q.Len())
	}

	// 创建同表管理器 → InitStruct 内 wireFailureReplay 立即重放队列。
	_ = NewAutoCacheBuilder[string, *itPlayer]().
		WithLongevityCache(time.Minute). // 周期 flush 不参与，落库只能来自 replay
		WithLongevityFailureQueue(q).
		New()

	nick, level, state, ok := env.queryPlayer("rp-1")
	if !ok {
		t.Fatal("启动 replay 后行未出现在 MySQL（replay 未真实执行 upsert）")
	}
	if nick != "from-queue" || level != 42 || state != 1 {
		t.Fatalf("replay 落库数据不符 nick=%v level=%v state=%v", nick, level, state)
	}
	if q.Len() != 0 {
		t.Fatalf("replay 成功后队列应清空, got %d", q.Len())
	}
}

// TestIT_ReplayPayload_ManualReplay 验证导出的手动重放 API（业务自定义恢复流程用）
// 在真实 MySQL 上同样走通：管理器已注册表 flusher 后，ReplayPayload 单条落库成功。
func TestIT_ReplayPayload_ManualReplay(t *testing.T) {
	env := ensureIT(t)

	// 确保 it_player 的 replay flusher 已注册（任一同表管理器创建过即可）。
	_ = NewAutoCacheBuilder[string, *itPlayer]().
		WithLongevityCache(time.Minute).
		WithLongevityFailureQueue(NoopFailureQueue{}).
		New()

	values := []any{uint8(1), "rp-manual", "manual-replay", int64(7)}
	payload, err := encodeFailurePayload("it_player", values, 1)
	if err != nil {
		t.Fatalf("encodeFailurePayload 失败: %v", err)
	}
	if err = ReplayPayload(payload); err != nil {
		t.Fatalf("ReplayPayload 失败: %v", err)
	}
	if nick, level, _, ok := env.queryPlayer("rp-manual"); !ok || nick != "manual-replay" || level != 7 {
		t.Fatalf("手动 replay 后行未正确落库 ok=%v nick=%v level=%v", ok, nick, level)
	}
}
