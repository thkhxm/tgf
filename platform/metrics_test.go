package platform

// H1 metrics 包装测试（MemoryProvider 断言）：
//   1. 注册时自动包装——经注册表取能力实例调用即计数，业务无感；
//   2. 成功/失败分别计入 calls_total / fail_total，时延进 latency_ms；
//   3. audit 的文本与图片共用 audit 指标；
//   4. webhook 验签调用计数。
//
// 注意：平台指标对象是 sync.Once 延迟初始化（与 rpc/metrics_hooks.go 同模式），
// 本文件用 resetMetricsForTest 重置绑定，保证拿到 MemoryProvider 的句柄；
// 包内测试串行执行，重置无并发风险。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thkhxm/tgf/v2/metrics"
)

func TestMetricsWrapperCounts(t *testing.T) {
	resetForTest()
	resetMetricsForTest()
	p := metrics.NewMemoryProvider()
	metrics.SetProvider(p)
	defer func() {
		metrics.SetProvider(metrics.NoopProvider())
		resetMetricsForTest()
		resetForTest()
	}()

	errBoom := errors.New("platform down")
	fake := &Fake{
		FakeName: "m_all",
		VerifyLoginFunc: func(_ context.Context, credential string) (*PlatformIdentity, error) {
			if credential == "bad" {
				return nil, errBoom
			}
			return &PlatformIdentity{Platform: "m_all", OpenID: "u1"}, nil
		},
		AuditImageFunc: func(_ context.Context, _ string, _ []byte) (*AuditResult, error) {
			return nil, errBoom
		},
	}
	if err := Register(fake); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	ctx := context.Background()

	// login：1 成功 + 1 失败
	lp, ok := Login("m_all")
	if !ok {
		t.Fatal("m_all 应具备 Login 能力")
	}
	if _, err := lp.VerifyLogin(ctx, "ok"); err != nil {
		t.Fatalf("VerifyLogin err: %v", err)
	}
	if _, err := lp.VerifyLogin(ctx, "bad"); err == nil {
		t.Fatal("VerifyLogin(bad) 应失败")
	}

	// payment：1 成功
	pp, ok := Payment("m_all")
	if !ok {
		t.Fatal("m_all 应具备 Payment 能力")
	}
	if _, err := pp.VerifyPayment(ctx, PaymentReceipt{OrderID: "o1"}); err != nil {
		t.Fatalf("VerifyPayment err: %v", err)
	}

	// audit：文本 1 成功 + 图片 1 失败（共用 audit 指标）
	ap, ok := Audit("m_all")
	if !ok {
		t.Fatal("m_all 应具备 Audit 能力")
	}
	if _, err := ap.AuditText(ctx, "u1", "hello"); err != nil {
		t.Fatalf("AuditText err: %v", err)
	}
	if _, err := ap.AuditImage(ctx, "u1", nil); err == nil {
		t.Fatal("AuditImage 应失败(注入)")
	}

	// webhook：1 成功
	wv, ok := Webhook("m_all")
	if !ok {
		t.Fatal("m_all 应具备 Webhook 能力")
	}
	if err := wv.VerifyWebhook(httptest.NewRequest(http.MethodPost, "/n", nil)); err != nil {
		t.Fatalf("VerifyWebhook err: %v", err)
	}

	// ---- 断言 ----
	asserts := []struct {
		metric string
		want   float64
	}{
		{"tgf_platform_login_calls_total", 2},
		{"tgf_platform_login_fail_total", 1},
		{"tgf_platform_payment_calls_total", 1},
		{"tgf_platform_payment_fail_total", 0},
		{"tgf_platform_audit_calls_total", 2},
		{"tgf_platform_audit_fail_total", 1},
		{"tgf_platform_webhook_calls_total", 1},
		{"tgf_platform_webhook_fail_total", 0},
	}
	for _, a := range asserts {
		if got := p.CounterValue(a.metric); got != a.want {
			t.Errorf("%v = %v, want %v", a.metric, got, a.want)
		}
	}

	histAsserts := []struct {
		metric  string
		wantObs int
	}{
		{"tgf_platform_login_latency_ms", 2},
		{"tgf_platform_payment_latency_ms", 1},
		{"tgf_platform_audit_latency_ms", 2},
		{"tgf_platform_webhook_latency_ms", 1},
	}
	for _, a := range histAsserts {
		if obs := p.HistogramObservations(a.metric); len(obs) != a.wantObs {
			t.Errorf("%v 观测数 = %d, want %d", a.metric, len(obs), a.wantObs)
		}
	}
}

// TestWrapWithMetricsCapabilityMatrix 穷举 16 种能力组合中的代表子集，
// 断言包装“不放大也不缩小”能力集（补充 TestCapabilityProbe 的注册表视角）。
func TestWrapWithMetricsCapabilityMatrix(t *testing.T) {
	tests := []struct {
		name        string
		p           Provider
		wantLogin   bool
		wantPayment bool
		wantAudit   bool
		wantWebhook bool
	}{
		{name: "仅Name", p: nameOnlyProvider{name: "n"}},
		{name: "仅Login", p: loginOnlyProvider{name: "l"}, wantLogin: true},
		{name: "仅Payment", p: paymentOnlyProvider{name: "p"}, wantPayment: true},
		{name: "仅Audit", p: auditOnlyProvider{name: "a"}, wantAudit: true},
		{name: "仅Webhook", p: webhookOnlyProvider{name: "w"}, wantWebhook: true},
		{name: "全能力", p: &Fake{}, wantLogin: true, wantPayment: true, wantAudit: true, wantWebhook: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := wrapWithMetrics(tt.p)
			if w.Name() != tt.p.Name() {
				t.Errorf("包装后 Name = %v, want %v", w.Name(), tt.p.Name())
			}
			if _, ok := w.(LoginProvider); ok != tt.wantLogin {
				t.Errorf("LoginProvider 断言 = %v, want %v", ok, tt.wantLogin)
			}
			if _, ok := w.(PaymentProvider); ok != tt.wantPayment {
				t.Errorf("PaymentProvider 断言 = %v, want %v", ok, tt.wantPayment)
			}
			if _, ok := w.(ContentAuditProvider); ok != tt.wantAudit {
				t.Errorf("ContentAuditProvider 断言 = %v, want %v", ok, tt.wantAudit)
			}
			if _, ok := w.(WebhookVerifier); ok != tt.wantWebhook {
				t.Errorf("WebhookVerifier 断言 = %v, want %v", ok, tt.wantWebhook)
			}
		})
	}
}
