//go:build integration
// +build integration

package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description E5 · 登录锁协调器（defaultLoginCoordinator）真实 Redis 集成测试
//
// gate.Login 的跨节点互斥（A3-phase2）由 loginCoord 承载；默认 tag 的
// gate_login_test.go 用 fake 协调器验证了编排顺序，本文件用真实 Redis 验证
// 生产实现 defaultLoginCoordinator 本体：
//   - AcquireLoginLock / ReleaseLoginLock 的互斥与释放（含内部重试退避耗尽）；
//   - GetGateOwner / SetGateOwner 的 user:node:meta 真实读写。
//
//2026/6/10
//***************************************************

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"context"

	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/db"
)

// ensureRedisForLoginLock 确保 db 层以 Redis 模式接入 harness 容器。
// 同一测试二进制里 ServeAdmin（admin_test.go）会把进程级 cacheModule 切到
// CacheModuleClose，这里必须显式切回再 Run。
func ensureRedisForLoginLock(t *testing.T) {
	t.Helper()
	ensureRPCIT(t)
	db.WithCacheModule(tgf.CacheModuleRedis)
	db.Run()
	if db.GetRedisClient() == nil {
		t.Fatal("db 层 Redis 初始化失败(GetRedisClient==nil)")
	}
}

func TestIT_LoginCoordinator_AcquireReleaseMutex(t *testing.T) {
	ensureRedisForLoginLock(t)
	coord := &defaultLoginCoordinator{}
	const uid = "it-rpc-lock-user"

	h, err := coord.AcquireLoginLock(uid)
	if err != nil {
		t.Fatalf("首次取登录锁失败: %v", err)
	}

	// 持锁期间再次获取：内部重试（loginLockRetries×backoff）耗尽后必须显式失败——
	// gate.Login 依赖该错误走"拒绝登录"分支而不是双在线。
	if _, err2 := coord.AcquireLoginLock(uid); err2 == nil {
		t.Fatal("持锁期间第二次 AcquireLoginLock 不应成功（跨节点互斥被破坏）")
	} else if !strings.Contains(err2.Error(), loginLockKey(uid)) {
		t.Fatalf("错误信息应包含锁 key 便于排障, got %v", err2)
	}

	// 释放后立刻可重取（ReleaseLoginLock 幂等）。
	coord.ReleaseLoginLock(h)
	coord.ReleaseLoginLock(h) // 第二次释放不得 panic
	h3, err3 := coord.AcquireLoginLock(uid)
	if err3 != nil {
		t.Fatalf("释放后重取登录锁失败: %v", err3)
	}
	coord.ReleaseLoginLock(h3)
}

func TestIT_LoginCoordinator_GateOwnerMetaRoundTrip(t *testing.T) {
	ensureRedisForLoginLock(t)
	coord := &defaultLoginCoordinator{}
	const uid = "it-rpc-owner-user"
	const addr = "tcp@10.0.0.42:8082"

	// 无 owner 时返回空串（首次登录分支）。
	db.DelNow(userNodeMetaKeyForIT(uid))
	if got := coord.GetGateOwner(uid); got != "" {
		t.Fatalf("无 owner 时应返回空串, got %q", got)
	}

	// 写入 owner 后读回一致（真实 Redis hash 往返，gate.Login 步骤 2/4 的依据）。
	coord.SetGateOwner(uid, addr)
	if got := coord.GetGateOwner(uid); got != addr {
		t.Fatalf("GetGateOwner = %q, want %q", got, addr)
	}
	// F3（审计8）：SetGateOwner 必须带 TTL——key 不再永久存活。
	if ttl := db.GetRedisClient().TTL(context.Background(), userNodeMetaKeyForIT(uid)).Val(); ttl <= 0 {
		t.Fatalf("owner meta key 应带 TTL, got %v", ttl)
	}
	// F3：ClearGateOwner 原子比对清理——地址不符不删,相符才删。
	coord.ClearGateOwner(uid, "tcp@other:9")
	if got := coord.GetGateOwner(uid); got != addr {
		t.Fatalf("地址不符的 ClearGateOwner 不应删除 owner, got %q", got)
	}
	coord.ClearGateOwner(uid, addr)
	if got := coord.GetGateOwner(uid); got != "" {
		t.Fatalf("地址相符的 ClearGateOwner 应删除 owner, got %q", got)
	}
	db.DelNow(userNodeMetaKeyForIT(uid))
}

// TestIT_LoginLock_WatchdogRenewalBeyondTTL F3 审计1 验收：watchdog 续期让锁的
// 有效持有时间可以超过初始 TTL（5s）——持锁 6.5s 后第二次获取仍然失败（锁未
// 静默过期），释放后立即可重取。
func TestIT_LoginLock_WatchdogRenewalBeyondTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("续期用例需要等待超过锁 TTL(≈6.5s),-short 跳过")
	}
	ensureRedisForLoginLock(t)
	coord := &defaultLoginCoordinator{}
	const uid = "it-rpc-lock-renewal-user"

	h, err := coord.AcquireLoginLock(uid)
	if err != nil {
		t.Fatalf("取登录锁失败: %v", err)
	}

	// 等待超过初始 TTL——没有 watchdog 时锁此刻已静默过期。
	time.Sleep(loginLockTTL + loginLockRefreshInterval)

	if _, err2 := coord.AcquireLoginLock(uid); err2 == nil {
		t.Fatal("持锁超过初始 TTL 后第二次获取不应成功——watchdog 续期失效(锁静默过期)")
	}

	coord.ReleaseLoginLock(h)
	h3, err3 := coord.AcquireLoginLock(uid)
	if err3 != nil {
		t.Fatalf("释放后重取登录锁失败: %v", err3)
	}
	coord.ReleaseLoginLock(h3)
}

// userNodeMetaKeyForIT 拼 user:node:meta 的 Redis key（与生产格式一致）。
func userNodeMetaKeyForIT(uid string) string {
	return fmt.Sprintf(tgf.RedisKeyUserNodeMeta, uid)
}

// TestIT_GateLogin_RealHMACAndRedisLock 登录鉴权安全用例（E5 行验收）——
// gate.Login 全链路：真实 HMAC token 校验（D7）× 真实 Redis 登录锁 / owner meta
// （A3-phase2，defaultLoginCoordinator），仅网络层 DoLogin 用 fakeTCPService 替代：
//  1. 凭据绑定他人身份（伪造 userId）→ ErrLoginUserIdMismatch 拒绝，绝不触达 DoLogin；
//  2. 无凭据 → ErrLoginTokenRequired 拒绝（fail-closed）；
//  3. 合法凭据 → 放行，真实走 Redis 锁取/放，owner meta 写入本节点地址。
func TestIT_GateLogin_RealHMACAndRedisLock(t *testing.T) {
	ensureRedisForLoginLock(t)

	resetLoginCheckForTest()
	t.Cleanup(resetLoginCheckForTest)
	(&Server{}).WithLoginTokenSecret("it-login-secret")

	// 真实协调器（同一测试二进制里其它用例可能换过 fake）+ 确定的本节点地址。
	origCoord := loginCoord
	loginCoord = &defaultLoginCoordinator{}
	t.Cleanup(func() { loginCoord = origCoord })
	origAddrFn := globalLocalAddressFn
	globalLocalAddressFn = func() string { return "tcp@127.0.0.1:7777" }
	t.Cleanup(func() { globalLocalAddressFn = origAddrFn })

	const uid = "it-gate-login-user"
	db.DelNow(userNodeMetaKeyForIT(uid))
	t.Cleanup(func() { db.DelNow(userNodeMetaKeyForIT(uid)) })

	fakeTCP := &fakeTCPService{}
	g := &GateService{}
	g.tcpService = fakeTCP

	// 1) 伪造身份：凭据绑定他人 uid → 拒绝且不触达登录态变更。
	forged, err := GenerateLoginToken("someone-else", time.Minute)
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	reply := &LoginRes{}
	if lerr := g.Login(context.Background(), &LoginReq{UserId: uid, TemplateUserId: "tpl", Token: forged}, reply); lerr == nil {
		t.Fatal("伪造 userId 的登录必须被拒绝")
	} else if !errors.Is(lerr, ErrLoginUserIdMismatch) {
		t.Fatalf("期望 ErrLoginUserIdMismatch, got %v", lerr)
	}
	if reply.ErrorCode != -1 {
		t.Fatalf("拒绝时 ErrorCode 应为 -1, got %d", reply.ErrorCode)
	}
	if len(fakeTCP.doLoginCalls) != 0 {
		t.Fatal("被拒登录绝不能触达 DoLogin")
	}

	// 2) 无凭据 → fail-closed 拒绝。
	reply = &LoginRes{}
	if lerr := g.Login(context.Background(), &LoginReq{UserId: uid, TemplateUserId: "tpl"}, reply); !errors.Is(lerr, ErrLoginTokenRequired) {
		t.Fatalf("无凭据应拒绝(ErrLoginTokenRequired), got %v", lerr)
	}

	// 3) 合法凭据 → 放行：真实 Redis 锁 + owner meta 写入。
	token, err := GenerateLoginToken(uid, time.Minute)
	if err != nil {
		t.Fatalf("签发 token 失败: %v", err)
	}
	reply = &LoginRes{}
	if lerr := g.Login(context.Background(), &LoginReq{UserId: uid, TemplateUserId: "tpl", Token: token}, reply); lerr != nil {
		t.Fatalf("合法凭据登录失败: %v", lerr)
	}
	if len(fakeTCP.doLoginCalls) != 1 || fakeTCP.doLoginCalls[0].UserId != uid {
		t.Fatalf("DoLogin 调用记录不符: %+v", fakeTCP.doLoginCalls)
	}
	coord := &defaultLoginCoordinator{}
	if owner := coord.GetGateOwner(uid); owner != "tcp@127.0.0.1:7777" {
		t.Fatalf("登录后 owner meta 应为本节点地址, got %q", owner)
	}
	// 登录锁已释放：可立即重取。
	h, lockErr := coord.AcquireLoginLock(uid)
	if lockErr != nil {
		t.Fatalf("登录完成后锁应已释放, 重取失败: %v", lockErr)
	}
	coord.ReleaseLoginLock(h)
}
