package platform

// H1 Fake 测试：
//   1. 全能力覆盖（编译期断言在 fake.go，运行期断言默认行为）；
//   2. 零值默认实现的确定性返回；
//   3. 字段函数注入后的透传（含错误注入）。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestFakeName(t *testing.T) {
	tests := []struct {
		name     string
		fakeName string
		want     string
	}{
		{name: "默认名", fakeName: "", want: "fake"},
		{name: "自定义名", fakeName: "wechat", want: "wechat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Fake{FakeName: tt.fakeName}
			if got := f.Name(); got != tt.want {
				t.Errorf("Name() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFakeDefaults(t *testing.T) {
	f := &Fake{FakeName: "dft"}
	ctx := context.Background()

	t.Run("VerifyLogin 默认身份", func(t *testing.T) {
		id, err := f.VerifyLogin(ctx, "code-1")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		want := &PlatformIdentity{Platform: "dft", OpenID: "fake-openid:code-1"}
		if !reflect.DeepEqual(id, want) {
			t.Errorf("VerifyLogin() = %+v, want %+v", id, want)
		}
	})

	t.Run("VerifyPayment 默认回显已支付", func(t *testing.T) {
		receipt := PaymentReceipt{
			OrderID: "o1", TransactionID: "t1", ProductID: "p1",
			Amount: 600, Currency: "CNY",
		}
		res, err := f.VerifyPayment(ctx, receipt)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !res.Paid || !res.Sandbox {
			t.Errorf("默认应 Paid=true Sandbox=true, got %+v", res)
		}
		if res.OrderID != "o1" || res.TransactionID != "t1" || res.ProductID != "p1" ||
			res.Amount != 600 || res.Currency != "CNY" {
			t.Errorf("默认应回显 receipt 字段, got %+v", res)
		}
	})

	t.Run("AuditText/AuditImage 默认通过", func(t *testing.T) {
		rt, err := f.AuditText(ctx, "u1", "hello")
		if err != nil || !rt.Pass || rt.Suggestion != SuggestionPass {
			t.Errorf("AuditText 默认应通过, got %+v err=%v", rt, err)
		}
		ri, err := f.AuditImage(ctx, "u1", []byte{0x89})
		if err != nil || !ri.Pass || ri.Suggestion != SuggestionPass {
			t.Errorf("AuditImage 默认应通过, got %+v err=%v", ri, err)
		}
	})

	t.Run("VerifyWebhook 默认通过", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/notify", nil)
		if err := f.VerifyWebhook(r); err != nil {
			t.Errorf("默认验签应通过, got %v", err)
		}
	})
}

func TestFakeProgrammable(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("boom")

	f := &Fake{
		FakeName: "prog",
		VerifyLoginFunc: func(_ context.Context, credential string) (*PlatformIdentity, error) {
			if credential == "bad" {
				return nil, errBoom
			}
			return &PlatformIdentity{Platform: "prog", OpenID: "custom", UnionID: "un1",
				SessionKey: "sk", Raw: map[string]string{"k": "v"}}, nil
		},
		VerifyPaymentFunc: func(_ context.Context, _ PaymentReceipt) (*PaymentResult, error) {
			return &PaymentResult{Platform: "prog", Paid: false}, nil
		},
		AuditTextFunc: func(_ context.Context, _, _ string) (*AuditResult, error) {
			return &AuditResult{Platform: "prog", Pass: false, Suggestion: SuggestionReject,
				Labels: []string{"ad"}}, nil
		},
		AuditImageFunc: func(_ context.Context, _ string, _ []byte) (*AuditResult, error) {
			return nil, errBoom
		},
		VerifyWebhookFunc: func(_ *http.Request) error {
			return errBoom
		},
	}

	t.Run("登录成功路径", func(t *testing.T) {
		id, err := f.VerifyLogin(ctx, "ok")
		if err != nil || id.OpenID != "custom" || id.UnionID != "un1" {
			t.Errorf("注入函数未透传, got %+v err=%v", id, err)
		}
	})
	t.Run("登录错误注入", func(t *testing.T) {
		if _, err := f.VerifyLogin(ctx, "bad"); !errors.Is(err, errBoom) {
			t.Errorf("应返回注入错误, got %v", err)
		}
	})
	t.Run("支付拒绝注入", func(t *testing.T) {
		res, err := f.VerifyPayment(ctx, PaymentReceipt{})
		if err != nil || res.Paid {
			t.Errorf("应返回 Paid=false, got %+v err=%v", res, err)
		}
	})
	t.Run("审核拒绝注入", func(t *testing.T) {
		res, err := f.AuditText(ctx, "u", "spam")
		if err != nil || res.Pass || res.Suggestion != SuggestionReject {
			t.Errorf("应返回拒绝, got %+v err=%v", res, err)
		}
		if _, err := f.AuditImage(ctx, "u", nil); !errors.Is(err, errBoom) {
			t.Errorf("应返回注入错误, got %v", err)
		}
	})
	t.Run("验签错误注入", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/notify", nil)
		if err := f.VerifyWebhook(r); !errors.Is(err, errBoom) {
			t.Errorf("应返回注入错误, got %v", err)
		}
	})
}
