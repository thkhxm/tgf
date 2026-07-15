package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description F3 · 断线重连（session resume + 重连窗口消息缓冲）测试
//
// 验收口径（路线图 F3）："断线 30s 内重连不丢推送"。
// 用真实 TCPServer + mockConn 驱动完整链路：
//   handleConn → DoLogin(签发 token) → 断线 detach → ToUser 缓冲 →
//   重连 DoLogin(带 token) → 缓冲按序补发。
//
//2026/6/10
//***************************************************

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/thkhxm/tgf/v2"
)

// startF3TestConn 启动一条 mockConn 连接并等待 handleConn 把它登记进 users 表，
// 返回 (conn, handleConn 退出信号, templateUserId)。
// 通过 before/after 快照 diff 找新 template 条目——server 上可能已有别的在线用户。
func startF3TestConn(t *testing.T, srv *TCPServer) (*mockConn, chan struct{}, string) {
	t.Helper()
	before := map[string]bool{}
	srv.users.Range(func(k string, _ IUserConnectData) bool {
		before[k] = true
		return true
	})

	mock := newMockConn(false)
	done := make(chan struct{})
	go func() {
		srv.handleConn(mock)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var tpl string
		srv.users.Range(func(k string, _ IUserConnectData) bool {
			if !before[k] {
				tpl = k
				return false
			}
			return true
		})
		if tpl != "" {
			return mock, done, tpl
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("连接未在 2s 内登记进 users 表")
	return nil, nil, ""
}

// recvFrameContaining 从 mock.writes 读帧直到取到包含 payload 的一帧（带超时）。
func recvFrameContaining(t *testing.T, mock *mockConn, payload string) []byte {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case w := <-mock.writes:
			if bytes.Contains(w, []byte(payload)) {
				return w
			}
		case <-deadline:
			t.Fatalf("2s 内未收到包含 %q 的下行帧", payload)
			return nil
		}
	}
}

// TestF3_Resume_NoLostPushWithinWindow 是 F3 验收主用例：断线期间的 ToUser 推送
// 进缓冲，窗口内凭 resume token 重连后按序补发，一条不丢。
func TestF3_Resume_NoLostPushWithinWindow(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	srv := newTestServer()
	const uid = "f3-resume-u"

	// 首次登录
	mock1, done1, tpl1 := startF3TestConn(t, srv)
	token1, err := srv.DoLogin(uid, tpl1, "")
	if err != nil {
		t.Fatalf("首次 DoLogin err = %v", err)
	}
	if token1 == "" {
		t.Fatalf("DoLogin 应签发 resume token")
	}

	// 在线推送直达
	if pushErr := srv.ToUser(uid, "demo.Push", []byte("push-online")); pushErr != nil {
		t.Fatalf("在线 ToUser err = %v", pushErr)
	}
	recvFrameContaining(t, mock1, "push-online")

	// 自然断线 → 进入重连窗口
	_ = mock1.Close()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatalf("断线后 handleConn 未退出")
	}
	if _, ok := srv.detached.Get(uid); !ok {
		t.Fatalf("自然断线后应存在 detached session")
	}

	// 断线期间推送 → 缓冲(延迟交付语义,返回 nil)
	for _, p := range []string{"push-offline-1", "push-offline-2", "push-offline-3"} {
		if pushErr := srv.ToUser(uid, "demo.Push", []byte(p)); pushErr != nil {
			t.Fatalf("断线窗口内 ToUser 应缓冲并返回 nil, got %v", pushErr)
		}
	}

	// 凭 token 重连 → 缓冲按序补发
	mock2, done2, tpl2 := startF3TestConn(t, srv)
	defer func() {
		_ = mock2.Close()
		<-done2
	}()
	token2, err := srv.DoLogin(uid, tpl2, token1)
	if err != nil {
		t.Fatalf("重连 DoLogin err = %v", err)
	}
	if token2 == "" || token2 == token1 {
		t.Fatalf("重连应签发新 token(旧 token 失效), old=%q new=%q", token1, token2)
	}

	// 按序收到全部缓冲推送
	for i, want := range []string{"push-offline-1", "push-offline-2", "push-offline-3"} {
		select {
		case w := <-mock2.writes:
			if !bytes.Contains(w, []byte(want)) {
				t.Fatalf("补发第 %d 帧顺序错乱: 期望包含 %q, got %q", i+1, want, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("2s 内未收到补发帧 %q", want)
		}
	}

	// detached 条目已消费
	if _, ok := srv.detached.Get(uid); ok {
		t.Fatalf("重连补发后 detached session 应被消费移除")
	}
}

// TestF3_Resume_WrongTokenDropsBuffer 验证令牌不匹配（新客户端实例 / 异地登录）
// 按全新会话处理：登录成功但缓冲被丢弃，不向新会话补发旧数据。
func TestF3_Resume_WrongTokenDropsBuffer(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	srv := newTestServer()
	const uid = "f3-wrong-token-u"

	mock1, done1, tpl1 := startF3TestConn(t, srv)
	if _, err := srv.DoLogin(uid, tpl1, ""); err != nil {
		t.Fatalf("DoLogin err = %v", err)
	}
	_ = mock1.Close()
	<-done1

	if err := srv.ToUser(uid, "demo.Push", []byte("stale-push")); err != nil {
		t.Fatalf("窗口内 ToUser err = %v", err)
	}

	mock2, done2, tpl2 := startF3TestConn(t, srv)
	defer func() {
		_ = mock2.Close()
		<-done2
	}()
	if _, err := srv.DoLogin(uid, tpl2, "not-the-token"); err != nil {
		t.Fatalf("令牌不符也应允许全新登录, err = %v", err)
	}

	select {
	case w := <-mock2.writes:
		t.Fatalf("令牌不符不应补发缓冲, 却收到 %q", w)
	case <-time.After(150 * time.Millisecond):
		// 正确:没有补发
	}
	if _, ok := srv.detached.Get(uid); ok {
		t.Fatalf("detached session 应被消费移除(缓冲丢弃)")
	}
}

// TestF3_Resume_WindowExpired 验证窗口到期后软状态自动回收：
// ToUser 恢复 ErrUserNotFound 语义，重连按全新会话处理。
func TestF3_Resume_WindowExpired(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	origWindow := resumeWindow
	resumeWindow = 50 * time.Millisecond
	defer func() { resumeWindow = origWindow }()

	srv := newTestServer()
	const uid = "f3-expired-u"

	mock1, done1, tpl1 := startF3TestConn(t, srv)
	token1, err := srv.DoLogin(uid, tpl1, "")
	if err != nil {
		t.Fatalf("DoLogin err = %v", err)
	}
	_ = mock1.Close()
	<-done1

	// 等窗口过期(定时器回收条目)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := srv.detached.Get(uid); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("重连窗口过期后 detached 条目未被回收")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := srv.ToUser(uid, "demo.Push", []byte("late-push")); !errors.Is(err, tgf.ErrUserNotFound) {
		t.Fatalf("窗口过期后 ToUser 应返回 ErrUserNotFound, got %v", err)
	}

	mock2, done2, tpl2 := startF3TestConn(t, srv)
	defer func() {
		_ = mock2.Close()
		<-done2
	}()
	if _, err := srv.DoLogin(uid, tpl2, token1); err != nil {
		t.Fatalf("窗口过期后凭旧 token 登录应按全新会话放行, err = %v", err)
	}
	select {
	case w := <-mock2.writes:
		t.Fatalf("窗口过期不应有补发, 却收到 %q", w)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestF3_Resume_BufferOverflowDropsOldest 验证缓冲上限语义：溢出丢最旧、保最新，
// 补发时只送出最近 resumeBufferLimit 条。
func TestF3_Resume_BufferOverflowDropsOldest(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	origLimit := resumeBufferLimit
	resumeBufferLimit = 3
	defer func() { resumeBufferLimit = origLimit }()

	srv := newTestServer()
	const uid = "f3-overflow-u"

	mock1, done1, tpl1 := startF3TestConn(t, srv)
	token1, err := srv.DoLogin(uid, tpl1, "")
	if err != nil {
		t.Fatalf("DoLogin err = %v", err)
	}
	_ = mock1.Close()
	<-done1

	for _, p := range []string{"of-1", "of-2", "of-3", "of-4", "of-5"} {
		if err := srv.ToUser(uid, "demo.Push", []byte(p)); err != nil {
			t.Fatalf("窗口内 ToUser err = %v", err)
		}
	}

	mock2, done2, tpl2 := startF3TestConn(t, srv)
	defer func() {
		_ = mock2.Close()
		<-done2
	}()
	if _, err := srv.DoLogin(uid, tpl2, token1); err != nil {
		t.Fatalf("重连 DoLogin err = %v", err)
	}

	// 只应补发最新 3 条:of-3, of-4, of-5
	for i, want := range []string{"of-3", "of-4", "of-5"} {
		select {
		case w := <-mock2.writes:
			if !bytes.Contains(w, []byte(want)) {
				t.Fatalf("溢出补发第 %d 帧期望 %q, got %q", i+1, want, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("未收到补发帧 %q", want)
		}
	}
	select {
	case w := <-mock2.writes:
		t.Fatalf("溢出后不应有更多补发, 却收到 %q", w)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestF3_Resume_FreshLoginWithoutTokenDropsBuffer 验证不带令牌的重新登录
// （典型:换设备登录）不补发——避免把旧设备的会话数据送进新设备。
func TestF3_Resume_FreshLoginWithoutTokenDropsBuffer(t *testing.T) {
	defer withFakeLoginCoord(t, &fakeLoginCoordinator{})()
	srv := newTestServer()
	const uid = "f3-fresh-u"

	mock1, done1, tpl1 := startF3TestConn(t, srv)
	if _, err := srv.DoLogin(uid, tpl1, ""); err != nil {
		t.Fatalf("DoLogin err = %v", err)
	}
	_ = mock1.Close()
	<-done1
	if err := srv.ToUser(uid, "demo.Push", []byte("old-device-push")); err != nil {
		t.Fatalf("窗口内 ToUser err = %v", err)
	}

	mock2, done2, tpl2 := startF3TestConn(t, srv)
	defer func() {
		_ = mock2.Close()
		<-done2
	}()
	if _, err := srv.DoLogin(uid, tpl2, ""); err != nil {
		t.Fatalf("无令牌登录 err = %v", err)
	}
	select {
	case w := <-mock2.writes:
		t.Fatalf("无令牌登录不应补发, 却收到 %q", w)
	case <-time.After(150 * time.Millisecond):
	}
}
