//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description H1：第三方平台 SDK 合约层——能力切面接口定义
//2026/6/11
//***************************************************

// Package platform 是第三方平台 SDK 的合约层（设计定稿见 doc/platform-sdk-design.md，v2.1 H 档）。
//
// 分层架构（核心合约接口 + 平台独立子模块 + 双部署形态）：
//   - 合约层（本包）：能力切面小接口 + 通用类型 + 注册表 + Fake——零第三方依赖；
//   - 实现层：github.com/thkhxm/tgf-platform/{wechat,tiktok,apple,facebook}，每个平台
//     独立 go module，实现其支持的合约子集——不接微信的项目，二进制里没有微信的
//     一行代码、go.mod 里没有它的任何依赖；
//   - 接入层：业务项目经 rpc.NewRPCServer().WithPlatform(wechat.New(cfg)) 进程内注册，
//     或部署独立 platform service（普通 IService 模块）经 RPC 调用。
//
// 能力探测：平台实现其支持的接口子集，框架/业务用注册表的能力取用函数
// （Login / Payment / Audit / Webhook）按平台名 + 能力获取，未实现该能力时第二个
// 返回值为 false——与 rpc 的可选子接口（IStatefulService / IUserLifecycleService）
// 同一模式。
//
// 可观测：Register 注册时自动套 metrics 包装（成功/失败/时延，复用 tgf/metrics，
// 见 metrics.go），业务无感；指标名 tgf_platform_<capability>_*。
//
// # 平台实现的工程纪律（硬规则，违反必被 review 打回）
//
// 接入任何平台 API 前（实战教训沉淀，见设计文档第四节）：
//  1. 先查官网最新文档，不许凭记忆写——endpoint / 鉴权头格式 / 参数单位必须以
//     官方文档为准；
//  2. 每个 endpoint 在代码注释附官方文档链接 + 拉取日期，例如：
//     “// TikTok code2session
//     // 文档：https://developer.open-tiktok.com/...（2026-06-11 拉取）
//     // 注意：access_token 必须用「用户 OAuth token」，不是 app client_credentials token。”
//  3. 接入完成必须用真凭据端到端验证一次——go build 通过不等于接通；
//  4. 调试持续报 “data invalid” 类错误时，优先怀疑 endpoint 选错 / SKU 不匹配 /
//     参数单位错（progress 0-1 还是 0-100、金额“分”还是“元”），而不是反复调 payload。
//
// 凭据来源：AppID / AppSecret 一律走配置系统 config.Current().Platform（E2 唯一
// 真源，Secret 类字段已登记 tgf/config.go sensitiveEnvKeys 启动日志脱敏），
// 绝不 os.Getenv 直读、绝不入库。
package platform

import (
	"context"
	"net/http"
)

// Provider 是所有平台实现的最小公分母。
type Provider interface {
	// Name 返回平台标识："wechat" / "tiktok" / "apple" / "facebook" / ...
	// 同一进程内必须唯一（注册表按它索引），约定小写短词。
	Name() string
}

// LoginProvider 平台登录凭据校验能力。
//
// 各平台映射：微信 jscode2session / TikTok code2session /
// Apple Sign-In identityToken 校验 / Facebook access_token 校验。
// credential 是客户端从平台 SDK 拿到的一次性凭据（wx.login 的 code、
// Apple 的 identityToken、FB 的 access_token 等），由实现按平台协议换取身份。
//
// 与框架登录的对接（设计文档第三节）：客户端把平台 credential 放进
// LoginReq.Token，业务用 rpc.WithLoginCheck 注入的校验器内部调 VerifyLogin
// 换 openid，再绑定/创建框架 userId。
type LoginProvider interface {
	Provider
	VerifyLogin(ctx context.Context, credential string) (*PlatformIdentity, error)
}

// PaymentProvider 支付校验与发货确认能力。
//
// 各平台映射：Apple App Store Server API 交易校验 / 微信米大师支付回调校验 /
// TikTok 虚拟支付 / Google Play Developer API。
// 实现必须以平台服务端 API 的应答为准判定 Paid——绝不信任客户端上报。
type PaymentProvider interface {
	Provider
	VerifyPayment(ctx context.Context, receipt PaymentReceipt) (*PaymentResult, error)
}

// ContentAuditProvider 内容安全审核能力（小游戏平台对 UGC 的强制要求）。
//
// 各平台映射：微信 security.msgSecCheck / security.mediaCheckAsync、
// 抖音小游戏内容安全检测。openID 是内容发布者的平台用户标识
// （部分平台要求随审核请求传入做风控关联）。
type ContentAuditProvider interface {
	Provider
	AuditText(ctx context.Context, openID, text string) (*AuditResult, error)
	AuditImage(ctx context.Context, openID string, image []byte) (*AuditResult, error)
}

// WebhookVerifier 平台服务端回调（支付通知等）的验签 + 防重放能力。
//
// 设计为可直接经 WebhookMiddleware 包装成 HTTP 中间件（与 web.Middleware
// 同构）挂在回调路由上：验签失败统一 401。
//
// 实现约定（硬要求）：
//   - VerifyWebhook 内必须完成防重放：校验平台时间戳（超窗口拒绝）+
//     对 nonce / 回调流水号去重；
//   - 实现若读取 r.Body 做验签，必须在返回前把 Body 重置回去
//     （r.Body = io.NopCloser(bytes.NewReader(raw))），否则业务 handler 读不到。
type WebhookVerifier interface {
	Provider
	VerifyWebhook(r *http.Request) error
}
