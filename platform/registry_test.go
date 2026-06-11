package platform

// H1 注册表测试：
//   1. Register 入参校验 + 重名拒绝（表驱动）；
//   2. 能力探测——实现子集的 provider 经注册（自动 metrics 包装）后断言能力集不放大不缩小；
//   3. List 全量 + 字典序；
//   4. 并发读写（启动期写 / 运行期读，-race 必过）。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// ---- 能力子集 fixture ----

// nameOnlyProvider 只实现最小公分母 Provider。
type nameOnlyProvider struct{ name string }

func (p nameOnlyProvider) Name() string { return p.name }

// loginOnlyProvider 只实现 Login 能力。
type loginOnlyProvider struct{ name string }

func (p loginOnlyProvider) Name() string { return p.name }
func (p loginOnlyProvider) VerifyLogin(_ context.Context, credential string) (*PlatformIdentity, error) {
	return &PlatformIdentity{Platform: p.name, OpenID: "login-only:" + credential}, nil
}

// paymentOnlyProvider 只实现 Payment 能力。
type paymentOnlyProvider struct{ name string }

func (p paymentOnlyProvider) Name() string { return p.name }
func (p paymentOnlyProvider) VerifyPayment(_ context.Context, receipt PaymentReceipt) (*PaymentResult, error) {
	return &PaymentResult{Platform: p.name, OrderID: receipt.OrderID, Paid: true}, nil
}

// auditOnlyProvider 只实现内容审核能力。
type auditOnlyProvider struct{ name string }

func (p auditOnlyProvider) Name() string { return p.name }
func (p auditOnlyProvider) AuditText(_ context.Context, _, _ string) (*AuditResult, error) {
	return &AuditResult{Platform: p.name, Pass: true, Suggestion: SuggestionPass}, nil
}
func (p auditOnlyProvider) AuditImage(_ context.Context, _ string, _ []byte) (*AuditResult, error) {
	return &AuditResult{Platform: p.name, Pass: true, Suggestion: SuggestionPass}, nil
}

// webhookOnlyProvider 只实现 Webhook 验签能力。
type webhookOnlyProvider struct {
	name string
	err  error
}

func (p webhookOnlyProvider) Name() string                        { return p.name }
func (p webhookOnlyProvider) VerifyWebhook(_ *http.Request) error { return p.err }

// emptyNameProvider Name() 返回空白串（非法）。
type emptyNameProvider struct{}

func (emptyNameProvider) Name() string { return "  " }

// ---- Register ----

func TestRegister(t *testing.T) {
	resetForTest()
	defer resetForTest()

	// 预置一个占位平台，给“重名”用例造冲突。
	if err := Register(nameOnlyProvider{name: "dup"}); err != nil {
		t.Fatalf("预置注册失败: %v", err)
	}

	tests := []struct {
		name    string
		p       Provider
		wantErr bool
	}{
		{name: "正常注册", p: &Fake{FakeName: "reg_ok"}, wantErr: false},
		{name: "nil Provider", p: nil, wantErr: true},
		{name: "空 Name", p: emptyNameProvider{}, wantErr: true},
		{name: "重名拒绝", p: nameOnlyProvider{name: "dup"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Register(tt.p)
			if (err != nil) != tt.wantErr {
				t.Errorf("Register() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	// 重名注册失败不能覆盖已有条目。
	if p, ok := Get("dup"); !ok || p.Name() != "dup" {
		t.Errorf("重名注册失败后原条目应保留, got ok=%v", ok)
	}
}

// ---- 能力探测（经注册表 + metrics 包装后断言）----

func TestCapabilityProbe(t *testing.T) {
	resetForTest()
	defer resetForTest()

	providers := []Provider{
		nameOnlyProvider{name: "cap_none"},
		loginOnlyProvider{name: "cap_login"},
		paymentOnlyProvider{name: "cap_payment"},
		auditOnlyProvider{name: "cap_audit"},
		webhookOnlyProvider{name: "cap_webhook"},
		&Fake{FakeName: "cap_all"},
	}
	for _, p := range providers {
		if err := Register(p); err != nil {
			t.Fatalf("注册 %v 失败: %v", p.Name(), err)
		}
	}

	tests := []struct {
		name        string
		wantLogin   bool
		wantPayment bool
		wantAudit   bool
		wantWebhook bool
	}{
		{name: "cap_none"},
		{name: "cap_login", wantLogin: true},
		{name: "cap_payment", wantPayment: true},
		{name: "cap_audit", wantAudit: true},
		{name: "cap_webhook", wantWebhook: true},
		{name: "cap_all", wantLogin: true, wantPayment: true, wantAudit: true, wantWebhook: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := Login(tt.name); ok != tt.wantLogin {
				t.Errorf("Login(%v) ok = %v, want %v", tt.name, ok, tt.wantLogin)
			}
			if _, ok := Payment(tt.name); ok != tt.wantPayment {
				t.Errorf("Payment(%v) ok = %v, want %v", tt.name, ok, tt.wantPayment)
			}
			if _, ok := Audit(tt.name); ok != tt.wantAudit {
				t.Errorf("Audit(%v) ok = %v, want %v", tt.name, ok, tt.wantAudit)
			}
			if _, ok := Webhook(tt.name); ok != tt.wantWebhook {
				t.Errorf("Webhook(%v) ok = %v, want %v", tt.name, ok, tt.wantWebhook)
			}
		})
	}

	// 未注册平台：四个能力取用都应 ok=false。
	if _, ok := Login("ghost"); ok {
		t.Error("未注册平台 Login 应返回 false")
	}

	// 包装委托正确性：经注册表取到的能力实例必须把调用透传给原实现。
	lp, ok := Login("cap_login")
	if !ok {
		t.Fatal("cap_login 应具备 Login 能力")
	}
	id, err := lp.VerifyLogin(context.Background(), "code123")
	if err != nil {
		t.Fatalf("VerifyLogin err: %v", err)
	}
	if id.OpenID != "login-only:code123" {
		t.Errorf("包装后 VerifyLogin 委托结果错: %v", id.OpenID)
	}
}

// ---- List ----

func TestList(t *testing.T) {
	resetForTest()
	defer resetForTest()

	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := Register(nameOnlyProvider{name: n}); err != nil {
			t.Fatalf("注册 %v 失败: %v", n, err)
		}
	}
	got := List()
	want := []string{"alpha", "mid", "zeta"} // 字典序
	if len(got) != len(want) {
		t.Fatalf("List() 数量 = %d, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p.Name() != want[i] {
			t.Errorf("List()[%d] = %v, want %v", i, p.Name(), want[i])
		}
	}
}

// ---- 并发安全（go test -race ./platform 必过）----

func TestRegistryConcurrent(t *testing.T) {
	resetForTest()
	defer resetForTest()

	const writers = 16
	const readersPerWriter = 4

	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("conc_%02d", idx)
			if err := Register(&Fake{FakeName: name}); err != nil {
				errCh <- fmt.Errorf("并发注册 %v 失败: %w", name, err)
			}
		}(i)
		for j := 0; j < readersPerWriter; j++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				// 与写并发的读：结果 ok 与否都合法，但绝不能 race / panic。
				name := fmt.Sprintf("conc_%02d", idx)
				if lp, ok := Login(name); ok {
					if _, err := lp.VerifyLogin(context.Background(), "c"); err != nil {
						errCh <- fmt.Errorf("并发 VerifyLogin %v 失败: %w", name, err)
					}
				}
				_ = List()
			}(i)
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	if got := len(List()); got != writers {
		t.Errorf("并发注册后 List() 数量 = %d, want %d", got, writers)
	}
}

// ---- 重名错误可被 errors 处理链消费 ----

func TestRegisterDuplicateErrorMessage(t *testing.T) {
	resetForTest()
	defer resetForTest()

	if err := Register(nameOnlyProvider{name: "msg"}); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	err := Register(nameOnlyProvider{name: "msg"})
	if err == nil {
		t.Fatal("重名注册应返回 error")
	}
	var wrapped = fmt.Errorf("启动失败: %w", err)
	if !errors.Is(wrapped, err) {
		t.Error("重名错误应可被 %w 链向上传递")
	}
}
