package rpc

// H1 WithPlatform 接线测试：
//   1. builder 注册 → platform 注册表按平台名 + 能力可取（无需 Run）；
//   2. 多次调用注册多平台；
//   3. 重名注册 fail-fast（注入 platformExitFn 断言退出码 1）。
//
// 注意：platform 注册表是进程级全局且测试间不跨包重置——本文件所有平台名
// 用 rpcwp_ 前缀保证唯一，不与 platform 包自身的测试冲突。

import (
	"context"
	"testing"

	"github.com/thkhxm/tgf/v2/platform"
)

func TestWithPlatform_RegistersToRegistry(t *testing.T) {
	s := NewRPCServer()

	// 链式注册多平台（返回值必须仍是同一个 builder）。
	got := s.WithPlatform(&platform.Fake{FakeName: "rpcwp_wechat"}).
		WithPlatform(&platform.Fake{FakeName: "rpcwp_tiktok"})
	if got != s {
		t.Fatal("WithPlatform 应返回同一个 *Server 以支持链式调用")
	}

	// builder → 注册表：按平台名 + 能力切面可取。
	for _, name := range []string{"rpcwp_wechat", "rpcwp_tiktok"} {
		lp, ok := platform.Login(name)
		if !ok {
			t.Fatalf("platform.Login(%v) 应可取", name)
		}
		id, err := lp.VerifyLogin(context.Background(), "code-x")
		if err != nil {
			t.Fatalf("VerifyLogin err: %v", err)
		}
		if id.OpenID != "fake-openid:code-x" {
			t.Errorf("VerifyLogin 委托结果错: %v", id.OpenID)
		}
		if _, ok := platform.Payment(name); !ok {
			t.Errorf("platform.Payment(%v) 应可取", name)
		}
		if _, ok := platform.Audit(name); !ok {
			t.Errorf("platform.Audit(%v) 应可取", name)
		}
		if _, ok := platform.Webhook(name); !ok {
			t.Errorf("platform.Webhook(%v) 应可取", name)
		}
	}
}

func TestWithPlatform_DuplicateFailFast(t *testing.T) {
	origExit := platformExitFn
	defer func() { platformExitFn = origExit }()

	exitCode := -1
	platformExitFn = func(code int) { exitCode = code }

	s := NewRPCServer()
	s.WithPlatform(&platform.Fake{FakeName: "rpcwp_dup"})
	if exitCode != -1 {
		t.Fatalf("首次注册不应触发退出, exitCode=%d", exitCode)
	}

	// 重名注册：必须以非零码 fail-fast。
	s.WithPlatform(&platform.Fake{FakeName: "rpcwp_dup"})
	if exitCode != 1 {
		t.Errorf("重名注册应 exit(1), got %d", exitCode)
	}

	// 原条目不受影响。
	if _, ok := platform.Login("rpcwp_dup"); !ok {
		t.Error("重名注册失败后原平台应仍可取")
	}
}

func TestWithPlatform_NilFailFast(t *testing.T) {
	origExit := platformExitFn
	defer func() { platformExitFn = origExit }()

	exitCode := -1
	platformExitFn = func(code int) { exitCode = code }

	NewRPCServer().WithPlatform(nil)
	if exitCode != 1 {
		t.Errorf("nil Provider 应 exit(1), got %d", exitCode)
	}
}
