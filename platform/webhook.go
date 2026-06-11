//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description H1：Webhook 中间件桥——把 WebhookVerifier 包装成与 web.Middleware 同构的 HTTP 中间件
//2026/6/11
//***************************************************

package platform

import (
	"net/http"

	"github.com/thkhxm/tgf/v2/log"
	"go.uber.org/zap"
)

// WebhookMiddleware 把 WebhookVerifier 包装成 HTTP 中间件，挂在平台服务端回调
// （支付通知等）路由上做验签 + 防重放门禁。
//
// 签名与 web.Middleware（type Middleware func(http.Handler) http.Handler）同构——
// 本包不 import web（避免 web→platform 之外再添反向依赖），返回值可直接放进
// web.Options.Middlewares、router.Group 的中间件参数，或任何标准库风格的中间件链：
//
//	wv, ok := platform.Webhook("tiktok")
//	if !ok { /* 平台未注册或无 Webhook 能力，启动期处理 */ }
//	r.Group("/pay/tiktok", web.Middleware(platform.WebhookMiddleware(wv))).
//	    POST("/notify", notifyHandler)
//
// 行为：
//   - v == nil → 一律 503（fail-closed，与 web.Auth 未配置口令同语义）：
//     宁可让平台按其重试策略稍后再回调，也绝不放过未验签请求；
//   - v.VerifyWebhook(r) 返回 error → 401 拒绝。响应体固定文案不回显错误细节
//     （防探测），细节进 Warn 日志（tag=platform，含平台名 + path + 错误）；
//   - 验签通过 → 放行 next。
//
// 防重放说明：VerifyWebhook 的实现**必须**自行完成防重放——校验平台时间戳
// （超出窗口拒绝，窗口建议 ≤5 分钟）+ 对 nonce / 回调流水号去重（Redis SETNX 等）。
// 本中间件只统一“验签/重放失败 → 401”的拒绝语义，不替实现做时间窗与去重：
// 各平台的时间戳字段、nonce 位置、签名算法差异太大，在合约层强行抽象必然漏。
//
// Body 提示：实现若读取 r.Body 验签，必须在返回前重置 Body（见 WebhookVerifier
// 接口注释），否则 next 的业务 handler 读不到回调体。
//
// metrics 提示：经 platform.Webhook(name) 从注册表取到的 verifier 已带 metrics
// 包装（注册时自动包）；直接传裸实现则该路由的验签调用不计数。
func WebhookMiddleware(v WebhookVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if v == nil {
				// fail-closed：装配错误（verifier 没取到）绝不能变成“裸奔放行”。
				log.ErrorTagW("platform", "webhook 中间件未配置 verifier(nil),拒绝回调",
					zap.String("path", r.URL.Path))
				http.Error(w, "webhook verifier unavailable", http.StatusServiceUnavailable)
				return
			}
			if err := v.VerifyWebhook(r); err != nil {
				log.WarnTagW("platform", "webhook 验签失败",
					zap.String("platform", v.Name()),
					zap.String("path", r.URL.Path),
					zap.Error(err))
				http.Error(w, "webhook verify failed", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
