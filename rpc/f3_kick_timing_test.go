package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F3 · 踢人时序收尾测试
//
// 对应审计"跨节点踢人时序错配：本端只等 200ms，远端清理路径却有 1s 强制 sleep"：
//   1. gate.Login 的远端踢改为同步带 ack——KickRemoteOwner 返回（远端清理完成）
//      之后才进入 DoLogin，旧会话 OfflineHook 不再晚于新会话上线；
//   2. TCPServer.Offline(replace=true) 不再固定 sleep 1s，改为有界排空通知队列；
//   3. 自然断线走 owner meta 比对清理 + 重连窗口；被踢会话跳过（审计8 wiring）。
//
//2026/6/10
//***************************************************

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"context"
)

// ---- 事件顺序记录器 ----

type f3EventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *f3EventRecorder) add(ev string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *f3EventRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	copy(out, r.events)
	return out
}

// f3OrderingCoord 模拟"远端清理需要时间"的协调器：KickRemoteOwner 阻塞一段时间
// 后记录 ack 事件再返回——同步 ack 语义下 DoLogin 必须排在它之后。
type f3OrderingCoord struct {
	*fakeLoginCoordinator
	rec       *f3EventRecorder
	kickDelay time.Duration
}

func (c *f3OrderingCoord) KickRemoteOwner(address, userId string) error {
	time.Sleep(c.kickDelay)
	c.rec.add("kick-acked")
	return nil
}

// f3OrderingTCP 在 DoLogin 时记录事件，用于断言时序。
type f3OrderingTCP struct {
	*fakeTCPService
	rec *f3EventRecorder
}

func (s *f3OrderingTCP) DoLogin(userId, templateUserId, resumeToken string) (string, error) {
	s.rec.add("do-login")
	return s.fakeTCPService.DoLogin(userId, templateUserId, resumeToken)
}

// TestF3_KickSyncAck_RemoteCleanupBeforeDoLogin 验证踢人时序修复的核心契约：
// 远端 owner 的清理（KickRemoteOwner 返回 = 远端 gate.Offline 已完成，含旧会话
// OfflineHook）严格先于本地 DoLogin（新会话 LoginHook 的源头）。
// 原实现 Oneshot + sleep(200ms) 在远端清理耗时 > 200ms 时必然乱序。
func TestF3_KickSyncAck_RemoteCleanupBeforeDoLogin(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withFakeLocalAddr(t, "tcp@local:8082")()

	rec := &f3EventRecorder{}
	fc := &f3OrderingCoord{
		fakeLoginCoordinator: &fakeLoginCoordinator{ownerAddr: "tcp@remote:9091"},
		rec:                  rec,
		// 远端清理耗时设为 300ms——超过旧实现的 200ms 等待窗口，旧时序下必乱序。
		kickDelay: 300 * time.Millisecond,
	}
	defer withFakeLoginCoord(t, fc)()

	ts := &f3OrderingTCP{fakeTCPService: &fakeTCPService{}, rec: rec}
	g := &GateService{tcpService: ts}

	reply := &LoginRes{}
	if err := g.Login(context.Background(), &LoginReq{UserId: "f3-order-u", TemplateUserId: "tpl"}, reply); err != nil {
		t.Fatalf("Login err = %v", err)
	}

	events := rec.snapshot()
	if len(events) != 2 || events[0] != "kick-acked" || events[1] != "do-login" {
		t.Fatalf("踢人时序错配:期望 [kick-acked do-login], got %v", events)
	}
	if reply.ResumeToken == "" {
		t.Errorf("登录成功应签发 resume token")
	}
}

// TestF3_ReplaceKick_NoFixedSecondSleep 验证远端清理路径（TCPServer.Offline
// replace=true）不再固定 sleep 1s：通知帧仍然先于断连送达客户端，但整个清理
// 在数百毫秒内完成——这是同步 ack 踢人 RPC 可接受时延的前提。
func TestF3_ReplaceKick_NoFixedSecondSleep(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	srv := newTestServer()

	mock, done, tpl := startF3TestConn(t, srv)
	const uid = "f3-kick-drain-u"
	if _, err := srv.DoLogin(uid, tpl, ""); err != nil {
		t.Fatalf("DoLogin err = %v", err)
	}

	start := time.Now()
	if exists := srv.Offline(uid, true); !exists {
		t.Fatalf("Offline 应找到在线用户")
	}
	elapsed := time.Since(start)
	// 原实现固定 sleep 1s;现在通知排空典型毫秒级,上界 replaceNotifyDrainWait(200ms)。
	if elapsed >= 700*time.Millisecond {
		t.Fatalf("Offline(replace=true) 耗时 %v,疑似仍有固定 1s sleep", elapsed)
	}

	// 通知帧必须已送达客户端(先通知后断连的语义保留)。
	gotNotify := false
	for i := 0; i < 4 && !gotNotify; i++ {
		select {
		case w := <-mock.writes:
			if bytes.Equal(w, replaceLoginData) {
				gotNotify = true
			}
		case <-time.After(time.Second):
		}
	}
	if !gotNotify {
		t.Fatalf("客户端未收到 ReplaceLogin 通知帧")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("被踢连接的 handleConn 未退出")
	}
}

// TestF3_NaturalDisconnect_ClearsOwnerMeta 验证审计8 的下线清理 wiring：
// 自然断线（非替换踢人）的已登录会话退出时，比对清理 owner meta（带本节点地址）。
func TestF3_NaturalDisconnect_ClearsOwnerMeta(t *testing.T) {
	defer withFakeLocalAddr(t, "tcp@local:7070")()
	fc := &fakeLoginCoordinator{}
	defer withFakeLoginCoord(t, fc)()
	srv := newTestServer()

	mock, done, tpl := startF3TestConn(t, srv)
	const uid = "f3-clear-owner-u"
	if _, err := srv.DoLogin(uid, tpl, ""); err != nil {
		t.Fatalf("DoLogin err = %v", err)
	}

	// 自然断线
	_ = mock.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("handleConn 未退出")
	}

	if len(fc.clearOwnerArg) != 1 ||
		fc.clearOwnerArg[0].UserId != uid ||
		fc.clearOwnerArg[0].ExpectAddress != "tcp@local:7070" {
		t.Fatalf("自然断线应比对清理 owner meta, got %+v", fc.clearOwnerArg)
	}
}

// TestF3_ReplaceKick_DoesNotClearOwnerMeta 验证被替换踢掉的会话不清 owner——
// owner 由新登录方负责覆盖写；如果被踢方也清，会在"老 defer 晚于新登录"时
// 误删新 owner（ClearGateOwner 的比对删除是兜底，这里验证根本不调用）。
func TestF3_ReplaceKick_DoesNotClearOwnerMeta(t *testing.T) {
	defer withFakeLocalAddr(t, "tcp@local:7070")()
	fc := &fakeLoginCoordinator{}
	defer withFakeLoginCoord(t, fc)()
	srv := newTestServer()

	_, done, tpl := startF3TestConn(t, srv)
	const uid = "f3-kicked-no-clear-u"
	if _, err := srv.DoLogin(uid, tpl, ""); err != nil {
		t.Fatalf("DoLogin err = %v", err)
	}

	srv.Offline(uid, true)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("handleConn 未退出")
	}

	if len(fc.clearOwnerArg) != 0 {
		t.Fatalf("被踢会话不应清理 owner meta, got %+v", fc.clearOwnerArg)
	}
	if _, ok := srv.detached.Get(uid); ok {
		t.Fatalf("被踢会话不应进入断线重连窗口")
	}
}

// TestF3_GateLogin_DoLoginFailClearsKickedOwner 验证审计"DoLogin 失败不回滚
// owner"的修复：踢掉老 owner 后 DoLogin 失败 → 比对清理指向被踢节点的 owner，
// 不残留指向已死会话的脏 owner。
func TestF3_GateLogin_DoLoginFailClearsKickedOwner(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withFakeLocalAddr(t, "tcp@local:8082")()

	fc := &fakeLoginCoordinator{ownerAddr: "tcp@remote:9091"}
	defer withFakeLoginCoord(t, fc)()

	ts := &fakeTCPService{doLoginErr: errF3DoLogin}
	g := newTestGate(ts)

	reply := &LoginRes{}
	if err := g.Login(context.Background(), &LoginReq{UserId: "f3-fail-u", TemplateUserId: "tpl"}, reply); err == nil {
		t.Fatalf("DoLogin 失败时 Login 应返回错误")
	}
	if len(fc.clearOwnerArg) != 1 ||
		fc.clearOwnerArg[0].UserId != "f3-fail-u" ||
		fc.clearOwnerArg[0].ExpectAddress != "tcp@remote:9091" {
		t.Fatalf("DoLogin 失败后应比对清理被踢的 owner, got %+v", fc.clearOwnerArg)
	}
	// owner 不应被写成本节点(登录没成功)。
	if len(fc.setOwnerArgs) != 0 {
		t.Fatalf("登录失败不应 SetGateOwner, got %+v", fc.setOwnerArgs)
	}
}

var errF3DoLogin = errF3("f3: do login failed")

type errF3 string

func (e errF3) Error() string { return string(e) }
