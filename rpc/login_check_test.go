package rpc

// D7 / P0-6 安全用例：gate.Login 的强制凭据校验。
// 对应 V3 路线图 D7 验收标准：伪造 userId 登录被拒；token 过期被拒；
// 旧的无鉴权行为必须显式 opt-out 才保留。
//
// 复用 gate_login_test.go 的 fakeLoginCoordinator / fakeTCPService 基建。

import (
	"errors"
	"testing"
	"time"

	"github.com/thkhxm/tgf/rpc/internal"
	"golang.org/x/net/context"
)

// withLoginSecret 重置鉴权状态并注入显式密钥，返回恢复函数。
func withLoginSecret(t *testing.T, secret string) func() {
	t.Helper()
	resetLoginCheckForTest()
	loginTokenSecret.Store(secret)
	return func() { resetLoginCheckForTest() }
}

// newSecuredTestGate 构造测试网关 + 标准 fake 环境（锁恒成功、无 owner）。
func newSecuredTestGate(t *testing.T) (*GateService, *fakeTCPService, func()) {
	t.Helper()
	restoreKick := shortenRemoteKickWait(t)
	restoreAddr := withFakeLocalAddr(t, "tcp@local:8082")
	fc := &fakeLoginCoordinator{ownerAddr: ""}
	restoreCoord := withFakeLoginCoord(t, fc)
	ts := &fakeTCPService{}
	return newTestGate(ts), ts, func() {
		restoreCoord()
		restoreAddr()
		restoreKick()
	}
}

// TestGateLogin_DefaultRejectsEmptyToken 验证默认安全：鉴权开启（默认态）时
// 不带 Token 的登录请求被拒绝，DoLogin 不被调用——这是 v2 行为（信任自报 userId）
// 的反向断言。
func TestGateLogin_DefaultRejectsEmptyToken(t *testing.T) {
	defer withLoginSecret(t, "test-secret")()
	g, ts, cleanup := newSecuredTestGate(t)
	defer cleanup()

	reply := &LoginRes{}
	err := g.Login(context.Background(), &LoginReq{UserId: "victim", TemplateUserId: "tpl"}, reply)
	if !errors.Is(err, ErrLoginTokenRequired) {
		t.Fatalf("err = %v, want ErrLoginTokenRequired", err)
	}
	if reply.ErrorCode != -1 {
		t.Errorf("reply.ErrorCode = %d, want -1", reply.ErrorCode)
	}
	if len(ts.doLoginCalls) != 0 {
		t.Errorf("DoLogin should NOT be called on rejected login, got %v", ts.doLoginCalls)
	}
}

// TestGateLogin_ForgedUserIdRejected 是 P0-6 的核心安全回归：攻击者持有自己
// （attacker）的合法 token，却尝试以 victim 的 userId 登录——必须被拒绝。
func TestGateLogin_ForgedUserIdRejected(t *testing.T) {
	defer withLoginSecret(t, "test-secret")()
	g, ts, cleanup := newSecuredTestGate(t)
	defer cleanup()

	attackerToken, err := GenerateLoginToken("attacker", time.Hour)
	if err != nil {
		t.Fatalf("GenerateLoginToken: %v", err)
	}

	reply := &LoginRes{}
	err = g.Login(context.Background(),
		&LoginReq{UserId: "victim", TemplateUserId: "tpl", Token: attackerToken}, reply)
	if !errors.Is(err, ErrLoginUserIdMismatch) {
		t.Fatalf("err = %v, want ErrLoginUserIdMismatch", err)
	}
	if len(ts.doLoginCalls) != 0 {
		t.Errorf("forged login must not reach DoLogin, got %v", ts.doLoginCalls)
	}
}

// TestGateLogin_ExpiredTokenRejected 验证过期 token 被拒绝（D7 验收标准）。
func TestGateLogin_ExpiredTokenRejected(t *testing.T) {
	defer withLoginSecret(t, "test-secret")()
	g, ts, cleanup := newSecuredTestGate(t)
	defer cleanup()

	expired, err := internal.BuildLoginToken([]byte("test-secret"), "u1", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("BuildLoginToken: %v", err)
	}

	reply := &LoginRes{}
	err = g.Login(context.Background(),
		&LoginReq{UserId: "u1", TemplateUserId: "tpl", Token: expired}, reply)
	if !errors.Is(err, ErrLoginCheckRejected) {
		t.Fatalf("err = %v, want ErrLoginCheckRejected (expired token)", err)
	}
	if len(ts.doLoginCalls) != 0 {
		t.Errorf("expired token must not reach DoLogin")
	}
}

// TestGateLogin_TamperedTokenRejected 验证签名被篡改的 token 被拒绝。
func TestGateLogin_TamperedTokenRejected(t *testing.T) {
	defer withLoginSecret(t, "test-secret")()
	g, ts, cleanup := newSecuredTestGate(t)
	defer cleanup()

	token, err := GenerateLoginToken("u1", time.Hour)
	if err != nil {
		t.Fatalf("GenerateLoginToken: %v", err)
	}
	// 翻转签名最后一个字符
	last := token[len(token)-1]
	var flipped byte = 'f'
	if last == 'f' {
		flipped = '0'
	}
	tampered := token[:len(token)-1] + string(flipped)

	reply := &LoginRes{}
	err = g.Login(context.Background(),
		&LoginReq{UserId: "u1", TemplateUserId: "tpl", Token: tampered}, reply)
	if !errors.Is(err, ErrLoginCheckRejected) {
		t.Fatalf("err = %v, want ErrLoginCheckRejected (tampered token)", err)
	}
	if len(ts.doLoginCalls) != 0 {
		t.Errorf("tampered token must not reach DoLogin")
	}
}

// TestGateLogin_ValidTokenAccepted 验证合法 token 正常登录。
func TestGateLogin_ValidTokenAccepted(t *testing.T) {
	defer withLoginSecret(t, "test-secret")()
	g, ts, cleanup := newSecuredTestGate(t)
	defer cleanup()

	token, err := GenerateLoginToken("u1", time.Hour)
	if err != nil {
		t.Fatalf("GenerateLoginToken: %v", err)
	}

	reply := &LoginRes{}
	err = g.Login(context.Background(),
		&LoginReq{UserId: "u1", TemplateUserId: "tpl", Token: token}, reply)
	if err != nil {
		t.Fatalf("Login with valid token err = %v, want nil", err)
	}
	if len(ts.doLoginCalls) != 1 || ts.doLoginCalls[0].UserId != "u1" {
		t.Errorf("DoLogin not called correctly: %v", ts.doLoginCalls)
	}
}

// TestGateLogin_NoSecretFailClosed 验证 fail-closed：鉴权开启但密钥未配置时，
// 即使带了（任意）token 也拒绝登录，而不是放行。
func TestGateLogin_NoSecretFailClosed(t *testing.T) {
	resetLoginCheckForTest() // 默认态：鉴权开启、无密钥、无自定义校验
	defer resetLoginCheckForTest()
	g, ts, cleanup := newSecuredTestGate(t)
	defer cleanup()

	reply := &LoginRes{}
	err := g.Login(context.Background(),
		&LoginReq{UserId: "u1", TemplateUserId: "tpl", Token: "whatever"}, reply)
	if !errors.Is(err, ErrLoginCheckRejected) {
		t.Fatalf("err = %v, want ErrLoginCheckRejected (fail-closed without secret)", err)
	}
	if len(ts.doLoginCalls) != 0 {
		t.Errorf("login without configured secret must be rejected (fail-closed)")
	}
}

// TestGateLogin_OptOutRestoresLegacyBehavior 验证显式 opt-out：WithoutLoginCheck
// 之后空 Token 也可登录（v2 行为），且这是恢复旧行为的**唯一**途径。
func TestGateLogin_OptOutRestoresLegacyBehavior(t *testing.T) {
	resetLoginCheckForTest()
	defer resetLoginCheckForTest()
	(&Server{}).WithoutLoginCheck()

	g, ts, cleanup := newSecuredTestGate(t)
	defer cleanup()

	reply := &LoginRes{}
	err := g.Login(context.Background(), &LoginReq{UserId: "u1", TemplateUserId: "tpl"}, reply)
	if err != nil {
		t.Fatalf("Login after WithoutLoginCheck err = %v, want nil", err)
	}
	if len(ts.doLoginCalls) != 1 {
		t.Errorf("DoLogin should be called after opt-out, got %d", len(ts.doLoginCalls))
	}
}

// stubLoginCheck 是注入式自定义校验器。
type stubLoginCheck struct {
	okToken string
	uid     string
}

func (s stubLoginCheck) CheckLogin(token string) (bool, string) {
	if token == s.okToken {
		return true, s.uid
	}
	return false, ""
}

// TestGateLogin_CustomLoginCheck 验证业务自定义钩子注入点：
//   - 自定义校验通过且身份匹配 → 登录成功；
//   - 自定义校验拒绝 → 登录失败；
//   - 自定义校验返回的 uid 与请求不一致 → 防冒充拒绝。
func TestGateLogin_CustomLoginCheck(t *testing.T) {
	resetLoginCheckForTest()
	defer resetLoginCheckForTest()
	(&Server{}).WithLoginCheck(stubLoginCheck{okToken: "magic", uid: "u1"})

	cases := []struct {
		name    string
		userId  string
		token   string
		wantErr error // nil 表示期望成功
	}{
		{"通过且身份匹配", "u1", "magic", nil},
		{"校验拒绝", "u1", "wrong", ErrLoginCheckRejected},
		{"身份不匹配防冒充", "u2", "magic", ErrLoginUserIdMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, ts, cleanup := newSecuredTestGate(t)
			defer cleanup()

			reply := &LoginRes{}
			err := g.Login(context.Background(),
				&LoginReq{UserId: c.userId, TemplateUserId: "tpl", Token: c.token}, reply)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if len(ts.doLoginCalls) != 1 {
					t.Errorf("DoLogin calls = %d, want 1", len(ts.doLoginCalls))
				}
			} else {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				if len(ts.doLoginCalls) != 0 {
					t.Errorf("rejected login must not reach DoLogin")
				}
			}
		})
	}
}

// TestGateLogin_CustomCheckNoIdentityBinding 验证自定义校验返回空 uid 时
// （只验凭据、不绑定身份）放行请求 userId。
func TestGateLogin_CustomCheckNoIdentityBinding(t *testing.T) {
	resetLoginCheckForTest()
	defer resetLoginCheckForTest()
	(&Server{}).WithLoginCheck(stubLoginCheck{okToken: "magic", uid: ""})

	g, ts, cleanup := newSecuredTestGate(t)
	defer cleanup()

	reply := &LoginRes{}
	err := g.Login(context.Background(),
		&LoginReq{UserId: "any-user", TemplateUserId: "tpl", Token: "magic"}, reply)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(ts.doLoginCalls) != 1 || ts.doLoginCalls[0].UserId != "any-user" {
		t.Errorf("DoLogin not called with request userId: %v", ts.doLoginCalls)
	}
}

// TestGenerateLoginToken_NoSecret 验证密钥未配置时签发报错（而非生成弱 token）。
func TestGenerateLoginToken_NoSecret(t *testing.T) {
	resetLoginCheckForTest()
	defer resetLoginCheckForTest()

	if _, err := GenerateLoginToken("u1", time.Hour); !errors.Is(err, internal.ErrLoginTokenSecretEmpty) {
		t.Fatalf("err = %v, want ErrLoginTokenSecretEmpty", err)
	}
}

// TestWithLoginTokenSecret_PriorityOverEnv 验证显式注入密钥优先于环境变量
// （E 档迁移后环境变量经配置快照读取，改 env 需同步重建快照）。
func TestWithLoginTokenSecret_PriorityOverEnv(t *testing.T) {
	resetLoginCheckForTest()
	defer resetLoginCheckForTest()
	setEnvConfigForTest(t, EnvLoginTokenSecret, "env-secret")

	(&Server{}).WithLoginTokenSecret("explicit-secret")
	if got := string(resolveLoginTokenSecret()); got != "explicit-secret" {
		t.Errorf("secret = %q, want explicit-secret", got)
	}

	// 清掉显式注入后回落到环境变量
	loginTokenSecret.Store("")
	if got := string(resolveLoginTokenSecret()); got != "env-secret" {
		t.Errorf("secret = %q, want env-secret", got)
	}
}
