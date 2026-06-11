//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description H1：平台合约层通用类型——标准化身份 / 支付凭据 / 支付结果 / 审核结果
//2026/6/11
//***************************************************

package platform

import "time"

// PlatformIdentity 是平台登录校验成功后的标准化身份。
//
// 各平台字段映射：
//
//	| 字段       | 微信            | TikTok/抖音   | Apple        | Facebook    |
//	|------------|-----------------|---------------|--------------|-------------|
//	| OpenID     | openid          | open_id       | sub          | user id     |
//	| UnionID    | unionid         | union_id      | （无）       | （无）      |
//	| SessionKey | session_key     | session_key   | （无）       | （无）      |
type PlatformIdentity struct {
	// Platform 平台标识，与 Provider.Name() 一致。
	Platform string
	// OpenID 平台内唯一 id（微信 openid / TikTok open_id / Apple sub / FB user id）。
	OpenID string
	// UnionID 跨应用统一 id（平台支持时；微信开放平台 unionid / 抖音 union_id）。
	UnionID string
	// SessionKey 会话密钥（平台返回时；用于后续解密手机号等敏感数据）。
	// 注意：属凭据类数据，只允许留在服务端内存/受控存储，严禁下发客户端或打日志。
	SessionKey string
	// Raw 平台原始字段透传（不强行抽象；上面没覆盖到的平台特有字段从这里取）。
	Raw map[string]string
}

// PaymentReceipt 是发起支付校验时业务侧提交的标准化凭据。
//
// 各平台字段映射：
//   - Apple：Payload = App Store Server API 的 signedTransaction（JWS）；
//   - 微信米大师：TransactionID = transaction_id，Payload = 回调密文/通知体；
//   - TikTok 虚拟支付：TransactionID = 平台 order_id，Payload = 回调原文；
//   - Google Play：TransactionID = purchaseToken。
type PaymentReceipt struct {
	// Platform 平台标识（冗余校验位；实现应校验与自身 Name() 一致，防止串单）。
	Platform string
	// OrderID 业务侧订单号（框架/游戏服生成，用于回查与幂等发货）。
	OrderID string
	// TransactionID 平台侧交易号（Apple transactionId / 微信 transaction_id /
	// TikTok order_id / Google purchaseToken）。
	TransactionID string
	// OpenID 付款用户的平台标识（部分平台校验接口要求传入）。
	OpenID string
	// ProductID 商品 id（平台后台配置的商品标识，用于核对货不对板）。
	ProductID string
	// Amount 期望金额，单位是**最小货币单位**（人民币“分”/ 美元 cents）。
	// 单位纪律（实战教训）：各平台接口的金额单位不一，实现必须按官方文档换算
	// 后再与本字段核对，严禁凭记忆假设单位。
	Amount int64
	// Currency 货币代码（ISO 4217，如 "CNY" / "USD"）。
	Currency string
	// Payload 平台原始凭据/回调体（验签与二次校验的原材料，按平台语义解释）。
	Payload string
	// Raw 平台原始字段透传。
	Raw map[string]string
}

// PaymentResult 是支付校验完成后的标准化结果。
// 实现必须以平台服务端 API 应答为准填充，业务依据 Paid 与金额/商品核对结果发货。
type PaymentResult struct {
	// Platform 平台标识。
	Platform string
	// OrderID 业务侧订单号（回传 PaymentReceipt.OrderID，便于调用方关联）。
	OrderID string
	// TransactionID 平台侧交易号（以平台应答为准，可能与请求时不同——如补单场景）。
	TransactionID string
	// ProductID 平台确认的商品 id。
	ProductID string
	// Amount 平台确认的实付金额，单位最小货币单位（分/cents）。
	Amount int64
	// Currency 平台确认的货币代码（ISO 4217）。
	Currency string
	// Paid 平台是否确认支付成功。false 时业务严禁发货。
	Paid bool
	// Sandbox 是否沙箱/测试环境交易（Apple sandbox / 微信仿真等）。
	// 生产环境收到 Sandbox=true 的单据应按业务策略拦截或单独记账。
	Sandbox bool
	// PaidAt 平台确认的支付完成时间（平台返回时；零值表示平台未提供）。
	PaidAt time.Time
	// Raw 平台原始字段透传。
	Raw map[string]string
}

// 审核建议的标准化取值（AuditResult.Suggestion）。
// 各平台映射：微信 msgSecCheck 的 pass/review/risky、抖音内容检测的命中策略
// 由实现归一到这三档。
const (
	// SuggestionPass 内容通过，可直接放行。
	SuggestionPass = "pass"
	// SuggestionReview 平台建议人工复核（业务可先隐藏待审）。
	SuggestionReview = "review"
	// SuggestionReject 内容违规，必须拦截。
	SuggestionReject = "reject"
)

// AuditResult 是内容安全审核的标准化结果。
type AuditResult struct {
	// Platform 平台标识。
	Platform string
	// Pass 是否可直接放行（等价于 Suggestion == SuggestionPass）。
	// false 时看 Suggestion 区分“待人工复核”还是“必须拦截”。
	Pass bool
	// Suggestion 标准化处置建议：SuggestionPass / SuggestionReview / SuggestionReject。
	Suggestion string
	// Labels 命中的风险标签（涉政/色情/广告/辱骂……平台原始 label 归一或透传）。
	Labels []string
	// TraceID 平台侧请求 id（微信 trace_id / 抖音 log_id），向平台报障时使用。
	TraceID string
	// Raw 平台原始字段透传。
	Raw map[string]string
}
