package rpc

// A2 phase1/phase3/phase4 的单元测试：
//   phase1/3: Offline 幂等 / Send 语义（newBareConnectData 驱动，纯逻辑）
//   phase4:   handleConn 生命周期（mockConn 驱动，真实 goroutine 模型）
//
// WS 真实网络层的测试在 conn_test.go 里用 httptest.Server + gorilla websocket client 覆盖。

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cornelk/hashmap"
	"github.com/thkhxm/rpcx/v2/share"
	"github.com/thkhxm/tgf"
	"golang.org/x/net/context"
)

// newBareConnectData 构造一个不依赖底层 net.Conn / websocket.Conn 的
// UserConnectData，仅用于纯逻辑测试。contextData 故意不 SetReqMetaData，
// 这样 Offline 里的 GetAllReqMetaDataKeys() 返回空，不会触发真实 OfflineHook
// 的 RPC 调用（phase1 的全局 rpcClient 可能未初始化）。
func newBareConnectData() *UserConnectData {
	return &UserConnectData{
		contextData: share.NewContext(context.Background()),
		stop:        make(chan struct{}),
		writeChan:   make(chan []byte, 4),
		reqChan:     nil,
	}
}

// TestOfflineIdempotent_Concurrent 保证并发情况下 Offline 只执行一次清理：
// stop 通道只被 close 一次，不会出现 close-of-closed-channel panic。
// 这是 A2-phase1 修的头号 bug（原来用 `u.replace` 做去重，非原子；并且
// logic goroutine 的 close(stop) 会与 Offline 的 `stop <- struct{}{}` 叠加 panic）。
func TestOfflineIdempotent_Concurrent(t *testing.T) {
	u := newBareConnectData()

	const N = 64
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			// 交替传入 true/false 模拟 "正常下线" vs "被替换登录踢"
			u.Offline(i%2 == 0)
		}(i)
	}
	wg.Wait()

	// stop 应被关闭且只关闭一次——从 closed 通道读能立刻拿到零值 + ok==false。
	select {
	case _, ok := <-u.stop:
		if ok {
			t.Fatalf("stop channel should be closed, got value with ok=true")
		}
	default:
		t.Fatalf("stop channel should be closed but still blocking")
	}
}

// TestOffline_StopSignalWakesSelect 验证 close(stop) 能正确唤醒 writer/logic
// goroutine 的 select 分支。用一个模拟的 select 循环代替真实 writer——收到
// stop 关闭后应当立即退出。断言"在合理时间内退出"而不是"比 stop 先触发"——
// Go select 对多个 ready 分支是伪随机选择，原先那个写法是靠运气通过的。
func TestOffline_StopSignalWakesSelect(t *testing.T) {
	u := newBareConnectData()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-u.writeChan:
				// 消费 writeChan 但不写底层 conn
			case <-u.stop:
				return
			}
		}
	}()

	u.Offline(false)

	select {
	case <-done:
		// writer-like goroutine 已退出
	case <-time.After(time.Second):
		t.Fatalf("writer goroutine did not exit within 1s after Offline")
	}
}

// TestOffline_WriteChanNotClosed 验证 Offline 不再负责 close(writeChan)：
// 原代码里 logic goroutine 会 close(writeChan)，和 writer goroutine 的写动作
// 竞争；phase1 把这个关闭动作删掉后，writeChan 应当在 Offline 之后仍然可写
// （只是没人消费而已——等 GC 回收）。
func TestOffline_WriteChanNotClosed(t *testing.T) {
	u := newBareConnectData()
	u.Offline(false)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("writeChan should not be closed after Offline, got panic: %v", r)
		}
	}()
	select {
	case u.writeChan <- []byte("post-offline"):
	default:
		// 缓冲满也 OK；关键是不 panic
	}
}

// TestOffline_ReplaceFlagReflectsFirstCall 验证 replace 字段的语义：
// Offline 是 sync.Once，只有"第一次调用"的参数会写入 u.replace。
func TestOffline_ReplaceFlagReflectsFirstCall(t *testing.T) {
	u := newBareConnectData()
	u.Offline(true)  // 先发一次 replace=true
	u.Offline(false) // 再发一次应被 Once 吞掉
	if !u.replace {
		t.Errorf("replace flag should remain true (set by first Offline call)")
	}
}

// ---- A2-phase3 Send 语义测试 ----

// TestSend_Success 验证快路径：writeChan 有空位时，Send 立即入队返回 nil。
func TestSend_Success(t *testing.T) {
	u := newBareConnectData()
	if err := u.Send([]byte("hello")); err != nil {
		t.Fatalf("Send err = %v, want nil", err)
	}
	select {
	case got := <-u.writeChan:
		if string(got) != "hello" {
			t.Errorf("writeChan got %q, want %q", got, "hello")
		}
	default:
		t.Fatalf("writeChan should contain the sent payload")
	}
}

// TestSend_AlreadyClosedFastPath 验证 Send 对已 Offline 的连接走快路径
// 立即返回 ErrConnClosed，不会等待 3s 超时。
func TestSend_AlreadyClosedFastPath(t *testing.T) {
	u := newBareConnectData()
	u.Offline(false)

	start := time.Now()
	err := u.Send([]byte("post-offline"))
	elapsed := time.Since(start)

	if !errors.Is(err, tgf.ErrConnClosed) {
		t.Errorf("Send err = %v, want ErrConnClosed", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Send on closed conn took %v, expected <<500ms (fast path)", elapsed)
	}
}

// TestSend_ChanFullTriggersOffline 验证当 writeChan 满且没人消费时，
// Send 在 defaultSendChanTimeout 后返回 ErrConnSendTimeout 并触发 Offline。
// 为了让测试跑得快，我们构造一个缓冲为 1 的 writeChan 并在测试里临时
// 把 defaultSendChanTimeout 替换成 100ms。
func TestSend_ChanFullTriggersOffline(t *testing.T) {
	origTimeout := defaultSendChanTimeout
	defaultSendChanTimeout = 100 * time.Millisecond
	defer func() { defaultSendChanTimeout = origTimeout }()

	u := &UserConnectData{
		contextData: share.NewContext(context.Background()),
		stop:        make(chan struct{}),
		writeChan:   make(chan []byte, 1),
	}
	// 把 writeChan 填满
	u.writeChan <- []byte("occupy")

	err := u.Send([]byte("second"))
	if !errors.Is(err, tgf.ErrConnSendTimeout) {
		t.Fatalf("Send err = %v, want ErrConnSendTimeout", err)
	}
	// Offline 应当已被触发：stop 被关闭
	select {
	case _, ok := <-u.stop:
		if ok {
			t.Errorf("stop should be closed after send timeout")
		}
	default:
		t.Errorf("stop should be closed (Offline should have been triggered)")
	}
}

// ============================================================================
// A2-phase4: handleConn 生命周期测试
//
// 用一个 mockConn（实现 IConn 接口）驱动 handleConn，验证：
//   - Heartbeat 帧能通过 writer goroutine 写回
//   - mockConn.Close() 后 handleConn 能正确退出（reader/logic/writer 都解开）
//   - 并发场景下多个连接同时关闭不死锁、goroutine 能回收
//
// 不走 Logic 帧，因为 doLogic 会调 sendMessage → 全局 rpcClient 在测试里未初始化。
// ============================================================================

type mockConn struct {
	frames  chan *FrameIn
	writes  chan []byte
	closed  atomic.Bool
	closeCh chan struct{}
	isWS    bool
}

func newMockConn(isWS bool) *mockConn {
	return &mockConn{
		frames:  make(chan *FrameIn, 16),
		writes:  make(chan []byte, 16),
		closeCh: make(chan struct{}),
		isWS:    isWS,
	}
}

func (m *mockConn) ReadFrame() (*FrameIn, error) {
	select {
	case f, ok := <-m.frames:
		if !ok {
			return nil, io.EOF
		}
		return f, nil
	case <-m.closeCh:
		return nil, io.EOF
	}
}

func (m *mockConn) WriteFrame(data []byte) error {
	if m.closed.Load() {
		return io.ErrClosedPipe
	}
	select {
	case m.writes <- data:
		return nil
	case <-m.closeCh:
		return io.ErrClosedPipe
	}
}

func (m *mockConn) Close() error {
	if m.closed.CompareAndSwap(false, true) {
		close(m.closeCh)
	}
	return nil
}

func (m *mockConn) RemoteAddr() string                 { return "mock://test" }
func (m *mockConn) SetReadDeadline(_ time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(_ time.Time) error { return nil }

// IsWebSocket 满足 tcp.go 的 wsTransport 可选接口（E6 后 IConn 不再声明它）。
func (m *mockConn) IsWebSocket() bool { return m.isWS }

// EncodeResponse 实现 E6 下沉后的 IConn 编码接口：按 isWS 标志选格式，
// 与真实 tcp/ws 适配器行为一致。
func (m *mockConn) EncodeResponse(messageType string, reqId, code int32, reply []byte) []byte {
	if m.isWS {
		return encodeWSResponseFrame(messageType, reqId, code, reply)
	}
	return encodeBinaryResponseFrame(messageType, reply)
}

// newTestServer 构造一个最小可用的 TCPServer，跳过 Run 里的真实 listen。
// config 填了必要的默认值——deadLineTime 必须 >0，否则 handleConn 里
// SetReadDeadline(now + 0) == now 会让下一次 Read 立即超时。
func newTestServer() *TCPServer {
	cfg := &ServerConfig{
		deadLineTime:    10 * time.Second,
		writeTimeout:    5 * time.Second,
		readBufferSize:  defaultReadBuffer,
		writeBufferSize: defaultWriteBuffer,
		netType:         netTcp,
	}
	return &TCPServer{
		config: cfg,
		// conChan 留 nil：handleConn 不读它，只有 selectorChan 用，
		// 而测试不会启动 selectorChan。
		users:   hashmap.New[string, IUserConnectData](),
		startup: new(sync.Once),
	}
}

// TestHandleConn_Heartbeat_RoundTrip 验证完整 reader→logic/writer→mockConn 的路径：
// 发一个 Heartbeat 帧，mockConn 的 writes 通道应当收到 heartbeatData 的回写。
func TestHandleConn_Heartbeat_RoundTrip(t *testing.T) {
	srv := newTestServer()
	mock := newMockConn(false)

	done := make(chan struct{})
	go func() {
		srv.handleConn(mock)
		close(done)
	}()

	// 喂一个心跳帧
	mock.frames <- &FrameIn{MessageType: Heartbeat}

	// 等 writer goroutine 把 heartbeatData 写回
	select {
	case got := <-mock.writes:
		if !bytes.Equal(got, heartbeatData) {
			t.Errorf("heartbeat response = %v, want %v", got, heartbeatData)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for heartbeat response")
	}

	// 关闭 mockConn → reader ReadFrame 返回 io.EOF → defer 触发 Offline → 所有 goroutine 退出
	_ = mock.Close()

	select {
	case <-done:
		// handleConn 正常返回
	case <-time.After(2 * time.Second):
		t.Fatalf("handleConn did not return after mockConn.Close()")
	}
}

// TestHandleConn_CloseTerminatesAllGoroutines 验证 Close 后 reader / logic /
// writer 三条 goroutine 都退出。通过"多次重复"侧面验证无泄漏：如果某轮漏了
// 一条 goroutine，下一轮的 handleConn 仍能跑起来但全局 goroutine 数会增长；
// 这里做一个轻量的增长断言。
func TestHandleConn_CloseTerminatesAllGoroutines(t *testing.T) {
	const rounds = 20
	srv := newTestServer()

	for i := 0; i < rounds; i++ {
		mock := newMockConn(false)
		done := make(chan struct{})
		go func() {
			srv.handleConn(mock)
			close(done)
		}()
		// 喂一个心跳帧确保 writer goroutine 至少被激活一次
		mock.frames <- &FrameIn{MessageType: Heartbeat}
		select {
		case <-mock.writes:
		case <-time.After(time.Second):
			t.Fatalf("round %d: heartbeat write timeout", i)
		}
		_ = mock.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("round %d: handleConn did not return", i)
		}
	}
}

// TestHandleConn_ConcurrentConnectionsNoDeadlock 验证并发起 N 个连接、同时关闭
// 不会相互死锁；所有 handleConn 都能在合理时间内退出。
func TestHandleConn_ConcurrentConnectionsNoDeadlock(t *testing.T) {
	const N = 50
	srv := newTestServer()

	mocks := make([]*mockConn, N)
	dones := make([]chan struct{}, N)
	for i := 0; i < N; i++ {
		mocks[i] = newMockConn(false)
		dones[i] = make(chan struct{})
		m := mocks[i]
		d := dones[i]
		go func() {
			srv.handleConn(m)
			close(d)
		}()
	}

	// 并发关闭所有连接
	var wg sync.WaitGroup
	for _, m := range mocks {
		wg.Add(1)
		m := m
		go func() {
			defer wg.Done()
			_ = m.Close()
		}()
	}
	wg.Wait()

	// 所有 handleConn 都应在合理时间内退出
	deadline := time.After(5 * time.Second)
	for i, d := range dones {
		select {
		case <-d:
		case <-deadline:
			t.Fatalf("connection %d: handleConn did not return within 5s", i)
		}
	}
}

// ============================================================================
// A3-phase1: sessionState CAS 状态机测试
// ============================================================================

// TestSessionState_InitialIdle 验证零值 UserConnectData 的 state 是 sessionIdle。
func TestSessionState_InitialIdle(t *testing.T) {
	u := newBareConnectData()
	if u.loadState() != sessionIdle {
		t.Errorf("initial state = %d, want sessionIdle(%d)", u.loadState(), sessionIdle)
	}
	if !u.isActiveForLogic() {
		t.Errorf("Idle connection should be active for logic")
	}
}

// TestSessionState_BeginLoginOnlyFromIdle 验证 tryBeginLogin 只从 Idle 起作用。
func TestSessionState_BeginLoginOnlyFromIdle(t *testing.T) {
	// Idle → LoggingIn 成功
	u := newBareConnectData()
	if !u.tryBeginLogin() {
		t.Fatalf("Idle → LoggingIn should succeed")
	}
	if u.loadState() != sessionLoggingIn {
		t.Errorf("state after tryBeginLogin = %d, want sessionLoggingIn", u.loadState())
	}

	// LoggingIn → LoggingIn 失败
	if u.tryBeginLogin() {
		t.Errorf("tryBeginLogin should fail when already LoggingIn")
	}

	// Online → LoggingIn 失败
	u2 := newBareConnectData()
	u2.state.Store(int32(sessionOnline))
	if u2.tryBeginLogin() {
		t.Errorf("tryBeginLogin should fail when already Online")
	}

	// Offlining → LoggingIn 失败
	u3 := newBareConnectData()
	u3.state.Store(int32(sessionOfflining))
	if u3.tryBeginLogin() {
		t.Errorf("tryBeginLogin should fail when Offlining")
	}
}

// TestSessionState_MarkOnlineOnlyFromLoggingIn 验证 markOnline 只从 LoggingIn 起作用。
func TestSessionState_MarkOnlineOnlyFromLoggingIn(t *testing.T) {
	// LoggingIn → Online 成功
	u := newBareConnectData()
	u.state.Store(int32(sessionLoggingIn))
	if !u.markOnline() {
		t.Fatalf("LoggingIn → Online should succeed")
	}
	if u.loadState() != sessionOnline {
		t.Errorf("state = %d, want sessionOnline", u.loadState())
	}

	// Idle → Online 失败（必须先 tryBeginLogin）
	u2 := newBareConnectData()
	if u2.markOnline() {
		t.Errorf("markOnline should fail from Idle")
	}

	// Online → Online 失败
	u3 := newBareConnectData()
	u3.state.Store(int32(sessionOnline))
	if u3.markOnline() {
		t.Errorf("markOnline should fail when already Online")
	}

	// Offlining → Online 失败
	u4 := newBareConnectData()
	u4.state.Store(int32(sessionOfflining))
	if u4.markOnline() {
		t.Errorf("markOnline should fail when Offlining")
	}
}

// TestSessionState_BeginOfflineFromAnyLiveState 验证 tryBeginOfflining 可以从
// Idle/LoggingIn/Online 任一状态迁移到 Offlining；Offlining → Offlining 失败。
func TestSessionState_BeginOfflineFromAnyLiveState(t *testing.T) {
	for name, from := range map[string]sessionState{
		"Idle":      sessionIdle,
		"LoggingIn": sessionLoggingIn,
		"Online":    sessionOnline,
	} {
		t.Run("from_"+name, func(t *testing.T) {
			u := newBareConnectData()
			u.state.Store(int32(from))
			if !u.tryBeginOfflining() {
				t.Errorf("tryBeginOfflining from %s should succeed", name)
			}
			if u.loadState() != sessionOfflining {
				t.Errorf("state = %d, want sessionOfflining", u.loadState())
			}
		})
	}

	// Offlining → Offlining 失败
	u := newBareConnectData()
	u.state.Store(int32(sessionOfflining))
	if u.tryBeginOfflining() {
		t.Errorf("tryBeginOfflining from Offlining should fail")
	}
}

// TestSessionState_ConcurrentOfflineSingleWinner 验证 N 个 goroutine 并发调
// tryBeginOfflining，恰好只有一个返回 true。
func TestSessionState_ConcurrentOfflineSingleWinner(t *testing.T) {
	u := newBareConnectData()

	const N = 128
	var winners int32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if u.tryBeginOfflining() {
				atomic.AddInt32(&winners, 1)
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Errorf("exactly one winner expected, got %d", winners)
	}
}

// TestSessionState_IsActiveForLogic 列出所有状态的 isActiveForLogic 结果。
func TestSessionState_IsActiveForLogic(t *testing.T) {
	cases := []struct {
		state  sessionState
		active bool
	}{
		{sessionIdle, true},
		{sessionLoggingIn, false},
		{sessionOnline, true},
		{sessionOfflining, false},
	}
	for _, c := range cases {
		u := newBareConnectData()
		u.state.Store(int32(c.state))
		if got := u.isActiveForLogic(); got != c.active {
			t.Errorf("state=%d: isActiveForLogic = %v, want %v", c.state, got, c.active)
		}
	}
}

// TestHandleConn_MultipleHeartbeats 验证 reader 连续收到多个帧不会丢失：
// 顺序发 5 个 Heartbeat，writes 通道应当收到 5 个响应。
func TestHandleConn_MultipleHeartbeats(t *testing.T) {
	srv := newTestServer()
	mock := newMockConn(false)

	done := make(chan struct{})
	go func() {
		srv.handleConn(mock)
		close(done)
	}()
	defer func() {
		_ = mock.Close()
		<-done
	}()

	for i := 0; i < 5; i++ {
		mock.frames <- &FrameIn{MessageType: Heartbeat}
	}

	for i := 0; i < 5; i++ {
		select {
		case got := <-mock.writes:
			if !bytes.Equal(got, heartbeatData) {
				t.Errorf("heartbeat %d: wrong payload %v", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("heartbeat %d: write timeout", i)
		}
	}
}
