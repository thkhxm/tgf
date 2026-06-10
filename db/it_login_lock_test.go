//go:build integration
// +build integration

package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description E5 · 分布式登录锁（真实 Redis / redislock）
//
// rpc.defaultLoginCoordinator 的登录互斥建立在 db.NewLock / db.UnLock 之上
// （key 形如 tgf:gate:login:lock:<uid>，TTL 5s）。本文件在真实 Redis 上验证
// 该原语的互斥/释放/并发语义；coordinator 自身的重试与编排在 rpc 包有
// fake 注入的单测（gate_login_test.go）与真实 Redis 集成测试
// （rpc/it_login_lock_test.go）双重覆盖。
//
//2026/6/10
//***************************************************

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bsm/redislock"
)

func TestIT_LoginLock_MutualExclusionAndRelease(t *testing.T) {
	ensureIT(t)
	const key = "tgf:gate:login:lock:it-user-1"

	l1, err := NewLock(key)
	if err != nil {
		t.Fatalf("首次取锁失败: %v", err)
	}

	// 持锁期间第二次取锁必须失败，且错误是 redislock.ErrNotObtained（可识别可重试）。
	if _, err2 := NewLock(key); err2 == nil {
		t.Fatal("持锁期间第二次取锁不应成功（互斥被破坏 → 双登录风险）")
	} else if !errors.Is(err2, redislock.ErrNotObtained) {
		t.Fatalf("第二次取锁应返回 redislock.ErrNotObtained, got %v", err2)
	}

	// 释放后立刻可重取。
	UnLock(l1)
	l3, err3 := NewLock(key)
	if err3 != nil {
		t.Fatalf("释放后重取锁失败: %v", err3)
	}
	UnLock(l3)
}

// TestIT_LoginLock_ConcurrentSingleWinner 模拟同一 uid 在多个网关节点并发登录：
// N 路并发抢同一把锁，恰好一路成功——跨节点登录互斥的核心保证（A3-phase2 契约）。
func TestIT_LoginLock_ConcurrentSingleWinner(t *testing.T) {
	ensureIT(t)
	const key = "tgf:gate:login:lock:it-user-concurrent"
	const n = 20

	var winners atomic.Int32
	var winnerLock *redislock.Lock
	var mu sync.Mutex
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			if l, err := NewLock(key); err == nil {
				winners.Add(1)
				mu.Lock()
				winnerLock = l
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("并发取锁应恰好 1 路成功, got %d（互斥被破坏）", got)
	}
	mu.Lock()
	UnLock(winnerLock)
	mu.Unlock()
}
