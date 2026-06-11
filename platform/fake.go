//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description H1：Fake——合约层自带的可编程全能力平台实现，供业务测试注入
//2026/6/11
//***************************************************

package platform

import (
	"context"
	"net/http"
)

// 编译期断言：Fake 必须实现全部能力切面（设计文档：合约包自带可编程 Fake，
// 业务测试不打真实平台 API）。
var (
	_ Provider             = (*Fake)(nil)
	_ LoginProvider        = (*Fake)(nil)
	_ PaymentProvider      = (*Fake)(nil)
	_ ContentAuditProvider = (*Fake)(nil)
	_ WebhookVerifier      = (*Fake)(nil)
)

// Fake 是可编程的全能力平台实现，供业务单测注入返回值/错误，
// 避免测试打真实平台 API：
//
//	f := &platform.Fake{
//	    VerifyLoginFunc: func(ctx context.Context, credential string) (*platform.PlatformIdentity, error) {
//	        return &platform.PlatformIdentity{Platform: "fake", OpenID: "u1"}, nil
//	    },
//	}
//	_ = platform.Register(f) // 或 rpc.NewRPCServer().WithPlatform(f)
//
// 零值可用：未注入的字段函数走确定性默认实现（见各方法注释）——
// 业务测试只需编程它关心的能力。
type Fake struct {
	// FakeName 平台标识；为空时 Name() 返回 "fake"。
	FakeName string
	// VerifyLoginFunc 注入 VerifyLogin 行为；nil 时返回确定性默认身份。
	VerifyLoginFunc func(ctx context.Context, credential string) (*PlatformIdentity, error)
	// VerifyPaymentFunc 注入 VerifyPayment 行为；nil 时回显凭据并标记已支付（沙箱）。
	VerifyPaymentFunc func(ctx context.Context, receipt PaymentReceipt) (*PaymentResult, error)
	// AuditTextFunc 注入 AuditText 行为；nil 时一律通过。
	AuditTextFunc func(ctx context.Context, openID, text string) (*AuditResult, error)
	// AuditImageFunc 注入 AuditImage 行为；nil 时一律通过。
	AuditImageFunc func(ctx context.Context, openID string, image []byte) (*AuditResult, error)
	// VerifyWebhookFunc 注入 VerifyWebhook 行为；nil 时一律验签通过。
	VerifyWebhookFunc func(r *http.Request) error
}

// Name 返回平台标识；FakeName 为空时返回 "fake"。
func (f *Fake) Name() string {
	if f.FakeName == "" {
		return "fake"
	}
	return f.FakeName
}

// VerifyLogin 实现 LoginProvider。VerifyLoginFunc 为 nil 时返回确定性默认身份：
// OpenID = "fake-openid:" + credential（便于测试按入参断言）。
func (f *Fake) VerifyLogin(ctx context.Context, credential string) (*PlatformIdentity, error) {
	if f.VerifyLoginFunc != nil {
		return f.VerifyLoginFunc(ctx, credential)
	}
	return &PlatformIdentity{
		Platform: f.Name(),
		OpenID:   "fake-openid:" + credential,
	}, nil
}

// VerifyPayment 实现 PaymentProvider。VerifyPaymentFunc 为 nil 时回显 receipt
// 的订单字段并返回 Paid=true / Sandbox=true（标记沙箱，提醒这不是真实校验）。
func (f *Fake) VerifyPayment(ctx context.Context, receipt PaymentReceipt) (*PaymentResult, error) {
	if f.VerifyPaymentFunc != nil {
		return f.VerifyPaymentFunc(ctx, receipt)
	}
	return &PaymentResult{
		Platform:      f.Name(),
		OrderID:       receipt.OrderID,
		TransactionID: receipt.TransactionID,
		ProductID:     receipt.ProductID,
		Amount:        receipt.Amount,
		Currency:      receipt.Currency,
		Paid:          true,
		Sandbox:       true,
	}, nil
}

// AuditText 实现 ContentAuditProvider。AuditTextFunc 为 nil 时一律通过。
func (f *Fake) AuditText(ctx context.Context, openID, text string) (*AuditResult, error) {
	if f.AuditTextFunc != nil {
		return f.AuditTextFunc(ctx, openID, text)
	}
	return &AuditResult{Platform: f.Name(), Pass: true, Suggestion: SuggestionPass}, nil
}

// AuditImage 实现 ContentAuditProvider。AuditImageFunc 为 nil 时一律通过。
func (f *Fake) AuditImage(ctx context.Context, openID string, image []byte) (*AuditResult, error) {
	if f.AuditImageFunc != nil {
		return f.AuditImageFunc(ctx, openID, image)
	}
	return &AuditResult{Platform: f.Name(), Pass: true, Suggestion: SuggestionPass}, nil
}

// VerifyWebhook 实现 WebhookVerifier。VerifyWebhookFunc 为 nil 时一律验签通过。
func (f *Fake) VerifyWebhook(r *http.Request) error {
	if f.VerifyWebhookFunc != nil {
		return f.VerifyWebhookFunc(r)
	}
	return nil
}
