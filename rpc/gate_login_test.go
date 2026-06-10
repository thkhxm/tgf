package rpc

// A3-phase2 mock 测试：gate.Login 的 5 条路径。
// 全部通过替换 package-level `loginCoord` 和构造 fakeTCPService 实现零依赖，
// 既不需要真实 Redis 也不需要真实 rpcx 节点。

import (
	"errors"
	"testing"

	"github.com/thkhxm/rpcx/v2/client"
	"golang.org/x/net/context"
)

// ---- fakeLoginCoordinator：记录调用顺序 + 允许注入预设结果 ----

type fakeLoginCoordinator struct {
	// 输入桩
	acquireErr error
	ownerAddr  string
	kickErr    error

	// 调用记录
	acquireCalls  int
	releaseCalls  int
	getOwnerArgs  []string
	setOwnerArgs  []fakeSetOwnerCall
	kickArgs      []fakeKickCall
	clearOwnerArg []fakeClearOwnerCall
}

type fakeSetOwnerCall struct {
	UserId  string
	Address string
}

type fakeKickCall struct {
	Address string
	UserId  string
}

type fakeClearOwnerCall struct {
	UserId        string
	ExpectAddress string
}

type fakeLockHandle struct{}

func (fakeLockHandle) isLoginLock() {}

func (f *fakeLoginCoordinator) AcquireLoginLock(userId string) (loginLockHandle, error) {
	f.acquireCalls++
	if f.acquireErr != nil {
		return nil, f.acquireErr
	}
	return fakeLockHandle{}, nil
}

func (f *fakeLoginCoordinator) ReleaseLoginLock(h loginLockHandle) {
	f.releaseCalls++
}

func (f *fakeLoginCoordinator) GetGateOwner(userId string) string {
	f.getOwnerArgs = append(f.getOwnerArgs, userId)
	return f.ownerAddr
}

func (f *fakeLoginCoordinator) SetGateOwner(userId, address string) {
	f.setOwnerArgs = append(f.setOwnerArgs, fakeSetOwnerCall{UserId: userId, Address: address})
}

func (f *fakeLoginCoordinator) ClearGateOwner(userId, expectAddress string) {
	f.clearOwnerArg = append(f.clearOwnerArg, fakeClearOwnerCall{UserId: userId, ExpectAddress: expectAddress})
}

func (f *fakeLoginCoordinator) KickRemoteOwner(address, userId string) error {
	f.kickArgs = append(f.kickArgs, fakeKickCall{Address: address, UserId: userId})
	return f.kickErr
}

// ---- fakeTCPService：最小实现 ITCPService，只记录 DoLogin/Offline 调用 ----

type fakeTCPService struct {
	doLoginErr     error
	doLoginCalls   []fakeDoLoginCall
	offlineCalls   []fakeOfflineCall
	offlineReturns bool
	// resumeTokenToIssue 是 DoLogin 成功时返回的 resume token（F3）。
	resumeTokenToIssue string
}

type fakeDoLoginCall struct {
	UserId, TemplateUserId string
	// ResumeToken 是调用方传入的重连令牌（F3）。
	ResumeToken string
}

type fakeOfflineCall struct {
	UserId  string
	Replace bool
}

func (f *fakeTCPService) Run() {}

func (f *fakeTCPService) UpdateUserNodeInfo(userId, servicePath, nodeId string) bool {
	return true
}

func (f *fakeTCPService) ToUser(userId, messageType string, data []byte) error {
	return nil
}

func (f *fakeTCPService) DoLogin(userId, templateUserId, resumeToken string) (string, error) {
	f.doLoginCalls = append(f.doLoginCalls, fakeDoLoginCall{
		UserId: userId, TemplateUserId: templateUserId, ResumeToken: resumeToken})
	if f.doLoginErr != nil {
		return "", f.doLoginErr
	}
	token := f.resumeTokenToIssue
	if token == "" {
		token = "fake-resume-token"
	}
	return token, nil
}

func (f *fakeTCPService) Offline(userId string, replace bool) (exists bool) {
	f.offlineCalls = append(f.offlineCalls, fakeOfflineCall{UserId: userId, Replace: replace})
	return f.offlineReturns
}

func (f *fakeTCPService) CloseListeners() error { return nil }

// ---- 测试辅助 ----

// withFakeLoginCoord 把 package-level loginCoord 临时替换成 fc，返回恢复函数。
func withFakeLoginCoord(t *testing.T, fc loginCoordinator) func() {
	t.Helper()
	orig := loginCoord
	loginCoord = fc
	return func() { loginCoord = orig }
}

// withFakeLocalAddr 临时覆盖 localGateAddress 的返回值。
func withFakeLocalAddr(t *testing.T, addr string) func() {
	t.Helper()
	orig := globalLocalAddressFn
	globalLocalAddressFn = func() string { return addr }
	return func() { globalLocalAddressFn = orig }
}

// F3：remoteKickWait 固定 sleep 已随"同步带 ack 的踢人"移除，
// 原 shortenRemoteKickWait 测试助手一并删除。

// withLoginCheckDisabled 在测试期间显式关闭 D7 登录凭据校验，返回恢复函数。
// 本文件的用例专注锁/踢人路径；鉴权自身的安全用例见 login_check_test.go。
func withLoginCheckDisabled(t *testing.T) func() {
	t.Helper()
	resetLoginCheckForTest()
	loginCheckDisabled.Store(true)
	return func() { resetLoginCheckForTest() }
}

// newTestGate 构造一个挂载了 fakeTCPService 的 GateService。
func newTestGate(ts *fakeTCPService) *GateService {
	return &GateService{tcpService: ts}
}

// 让 client.Call 参与但不触发链路（预留）
var _ = client.Call{}

// ---- 测试路径 ----

// 路径 1：首次登录，无 owner
func TestGateLogin_NoOwner(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withFakeLocalAddr(t, "tcp@local:8082")()

	fc := &fakeLoginCoordinator{ownerAddr: ""}
	defer withFakeLoginCoord(t, fc)()

	ts := &fakeTCPService{}
	g := newTestGate(ts)

	reply := &LoginRes{}
	err := g.Login(context.Background(), &LoginReq{UserId: "u1", TemplateUserId: "tpl1"}, reply)
	if err != nil {
		t.Fatalf("Login err = %v, want nil", err)
	}

	if fc.acquireCalls != 1 {
		t.Errorf("AcquireLoginLock calls = %d, want 1", fc.acquireCalls)
	}
	if fc.releaseCalls != 1 {
		t.Errorf("ReleaseLoginLock calls = %d, want 1", fc.releaseCalls)
	}
	if len(fc.kickArgs) != 0 {
		t.Errorf("no owner → KickRemoteOwner should not be called, got %v", fc.kickArgs)
	}
	if len(ts.offlineCalls) != 0 {
		t.Errorf("no owner → tcpService.Offline should not be called, got %v", ts.offlineCalls)
	}
	if len(ts.doLoginCalls) != 1 || ts.doLoginCalls[0].UserId != "u1" {
		t.Errorf("DoLogin not called correctly: %v", ts.doLoginCalls)
	}
	if len(fc.setOwnerArgs) != 1 ||
		fc.setOwnerArgs[0].UserId != "u1" ||
		fc.setOwnerArgs[0].Address != "tcp@local:8082" {
		t.Errorf("SetGateOwner wrong: %+v", fc.setOwnerArgs)
	}
}

// 路径 2：owner 是本地节点 → 本地踢
func TestGateLogin_LocalOwner(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withFakeLocalAddr(t, "tcp@local:8082")()

	fc := &fakeLoginCoordinator{ownerAddr: "tcp@local:8082"}
	defer withFakeLoginCoord(t, fc)()

	ts := &fakeTCPService{}
	g := newTestGate(ts)

	reply := &LoginRes{}
	if err := g.Login(context.Background(), &LoginReq{UserId: "u2", TemplateUserId: "tpl2"}, reply); err != nil {
		t.Fatalf("Login err = %v", err)
	}

	if len(fc.kickArgs) != 0 {
		t.Errorf("local owner → KickRemoteOwner should not be called, got %v", fc.kickArgs)
	}
	if len(ts.offlineCalls) != 1 ||
		ts.offlineCalls[0].UserId != "u2" ||
		!ts.offlineCalls[0].Replace {
		t.Errorf("local Offline call wrong: %v", ts.offlineCalls)
	}
	if len(ts.doLoginCalls) != 1 {
		t.Errorf("DoLogin should be called once, got %d", len(ts.doLoginCalls))
	}
}

// 路径 3：owner 是远端 → 定向踢远端
func TestGateLogin_RemoteOwner(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withFakeLocalAddr(t, "tcp@local:8082")()

	fc := &fakeLoginCoordinator{ownerAddr: "tcp@remote:9091"}
	defer withFakeLoginCoord(t, fc)()

	ts := &fakeTCPService{}
	g := newTestGate(ts)

	reply := &LoginRes{}
	if err := g.Login(context.Background(), &LoginReq{UserId: "u3", TemplateUserId: "tpl3"}, reply); err != nil {
		t.Fatalf("Login err = %v", err)
	}

	if len(fc.kickArgs) != 1 {
		t.Fatalf("KickRemoteOwner calls = %d, want 1", len(fc.kickArgs))
	}
	if fc.kickArgs[0].Address != "tcp@remote:9091" || fc.kickArgs[0].UserId != "u3" {
		t.Errorf("kick arg wrong: %+v", fc.kickArgs[0])
	}
	if len(ts.offlineCalls) != 0 {
		t.Errorf("remote owner → local Offline should not be called, got %v", ts.offlineCalls)
	}
	if len(ts.doLoginCalls) != 1 {
		t.Errorf("DoLogin should still be called after remote kick, got %d", len(ts.doLoginCalls))
	}
	if len(fc.setOwnerArgs) != 1 || fc.setOwnerArgs[0].Address != "tcp@local:8082" {
		t.Errorf("SetGateOwner should rewrite owner to local, got %+v", fc.setOwnerArgs)
	}
}

// 路径 4：远端踢失败仍继续本地登录（宽松策略）
func TestGateLogin_RemoteKickErrorContinues(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withFakeLocalAddr(t, "tcp@local:8082")()

	fc := &fakeLoginCoordinator{
		ownerAddr: "tcp@remote:9091",
		kickErr:   errors.New("connection refused"),
	}
	defer withFakeLoginCoord(t, fc)()

	ts := &fakeTCPService{}
	g := newTestGate(ts)

	reply := &LoginRes{}
	if err := g.Login(context.Background(), &LoginReq{UserId: "u4", TemplateUserId: "tpl4"}, reply); err != nil {
		t.Fatalf("Login should not fail on kick error: %v", err)
	}
	if len(ts.doLoginCalls) != 1 {
		t.Errorf("DoLogin should still run after kick error, got %d calls", len(ts.doLoginCalls))
	}
}

// 路径 5：获取锁失败 → 立刻返回 error 且 DoLogin 不被调用
func TestGateLogin_AcquireLockFail(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withFakeLocalAddr(t, "tcp@local:8082")()

	lockErr := errors.New("lock timeout")
	fc := &fakeLoginCoordinator{acquireErr: lockErr}
	defer withFakeLoginCoord(t, fc)()

	ts := &fakeTCPService{}
	g := newTestGate(ts)

	reply := &LoginRes{}
	err := g.Login(context.Background(), &LoginReq{UserId: "u5", TemplateUserId: "tpl5"}, reply)
	if err == nil {
		t.Fatalf("expected error when lock fails")
	}
	if reply.ErrorCode != -1 {
		t.Errorf("reply.ErrorCode = %d, want -1", reply.ErrorCode)
	}
	if fc.releaseCalls != 0 {
		t.Errorf("ReleaseLoginLock should not be called when acquire fails, got %d", fc.releaseCalls)
	}
	if len(ts.doLoginCalls) != 0 {
		t.Errorf("DoLogin should not be called when lock fails, got %d", len(ts.doLoginCalls))
	}
}

// 路径 6：DoLogin 失败 → 不写 owner，Release 仍被调用
func TestGateLogin_DoLoginFail(t *testing.T) {
	defer withLoginCheckDisabled(t)()
	defer withFakeLocalAddr(t, "tcp@local:8082")()

	fc := &fakeLoginCoordinator{ownerAddr: ""}
	defer withFakeLoginCoord(t, fc)()

	ts := &fakeTCPService{doLoginErr: errors.New("user not found")}
	g := newTestGate(ts)

	reply := &LoginRes{}
	err := g.Login(context.Background(), &LoginReq{UserId: "u6", TemplateUserId: "tpl6"}, reply)
	if err == nil {
		t.Fatalf("expected error when DoLogin fails")
	}
	if reply.ErrorCode != -1 {
		t.Errorf("reply.ErrorCode = %d, want -1", reply.ErrorCode)
	}
	if fc.releaseCalls != 1 {
		t.Errorf("ReleaseLoginLock should still be called via defer, got %d", fc.releaseCalls)
	}
	if len(fc.setOwnerArgs) != 0 {
		t.Errorf("SetGateOwner should not be called on DoLogin failure, got %+v", fc.setOwnerArgs)
	}
}

// loginLockKey 格式测试，确保后续 metrics/排查能识别。
func TestLoginLockKey(t *testing.T) {
	got := loginLockKey("user-42")
	want := "tgf:gate:login:lock:user-42"
	if got != want {
		t.Errorf("loginLockKey = %q, want %q", got, want)
	}
}
