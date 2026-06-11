package platform

// H1 Webhook 中间件测试：
//   1. 验签失败 → 401 且不放行；
//   2. 验签通过 → 200 放行 next；
//   3. nil verifier → 503 fail-closed；
//   4. 与 web.Middleware 签名同构（编译期断言；test-only import，不构成
//      platform→web 的生产依赖）。

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thkhxm/tgf/v2/web"
)

// 编译期断言：WebhookMiddleware 的返回值可直接当 web.Middleware 用
// （func(http.Handler) http.Handler 同构）。
var _ web.Middleware = WebhookMiddleware(nil)

func TestWebhookMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		verifier   WebhookVerifier
		wantStatus int
		wantNext   bool
	}{
		{
			name:       "验签通过放行",
			verifier:   &Fake{FakeName: "wh_ok"},
			wantStatus: http.StatusOK,
			wantNext:   true,
		},
		{
			name: "验签失败401",
			verifier: &Fake{FakeName: "wh_bad", VerifyWebhookFunc: func(_ *http.Request) error {
				return errors.New("签名不匹配")
			}},
			wantStatus: http.StatusUnauthorized,
			wantNext:   false,
		},
		{
			name:       "nil verifier 503 fail-closed",
			verifier:   nil,
			wantStatus: http.StatusServiceUnavailable,
			wantNext:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			h := WebhookMiddleware(tt.verifier)(next)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/pay/notify", nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if nextCalled != tt.wantNext {
				t.Errorf("next 被调用 = %v, want %v", nextCalled, tt.wantNext)
			}
		})
	}
}

// TestWebhookMiddlewareViaRegistry 验证从注册表取 verifier → 中间件 → 业务
// handler 的完整链路（设计文档第三节：回调路由验签的标准接法）。
func TestWebhookMiddlewareViaRegistry(t *testing.T) {
	resetForTest()
	defer resetForTest()

	rejected := errors.New("重放攻击: nonce 已使用")
	if err := Register(&Fake{FakeName: "wh_pay", VerifyWebhookFunc: func(r *http.Request) error {
		if r.Header.Get("X-Nonce") == "used" {
			return rejected
		}
		return nil
	}}); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	wv, ok := Webhook("wh_pay")
	if !ok {
		t.Fatal("wh_pay 应具备 Webhook 能力")
	}
	h := WebhookMiddleware(wv)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 正常回调放行
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/pay/notify", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("正常回调 status = %d, want 200", rec.Code)
	}

	// 重放回调 401
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pay/notify", nil)
	req.Header.Set("X-Nonce", "used")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("重放回调 status = %d, want 401", rec.Code)
	}
}
