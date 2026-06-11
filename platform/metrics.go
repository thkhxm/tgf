//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description H1：平台调用 metrics 包装——注册时自动包，成功/失败/时延，业务无感
//2026/6/11
//***************************************************

package platform

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/thkhxm/tgf/v2/metrics"
)

// 能力标识（指标名后缀 + 注册日志的 capabilities 字段取值）。
const (
	capLogin   = "login"
	capPayment = "payment"
	capAudit   = "audit"
	capWebhook = "webhook"
)

// capabilityMetrics 是单个能力切面的指标三件套。
type capabilityMetrics struct {
	calls     metrics.Counter
	fails     metrics.Counter
	latencyMs metrics.Histogram
}

// 指标对象延迟初始化（与 rpc/metrics_hooks.go 同一模式）：允许业务在
// Server.WithMetrics / metrics.SetProvider 之后、真正发起平台调用之前切换 Provider。
var (
	platformMetricsOnce sync.Once
	platformCapMetrics  map[string]*capabilityMetrics
)

// platformLatencyBuckets 针对平台出网调用的延迟画像：
// 平台 API 是跨公网 HTTP（几十到几百毫秒常态），桶位相比进程内 RPC 整体右移；
// 2500ms 以上基本是平台侧故障/网络病态，落 +Inf 桶。
var platformLatencyBuckets = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500}

// ensurePlatformMetrics 首次观测时创建全部能力的指标对象。
// 指标名固定为 tgf_platform_<capability>_{calls_total,fail_total,latency_ms}，
// 有限集合、启动期一次性创建（符合 metrics 包“name 稳定、不动态拼接”的约定）。
// label 维度（platform）按 tgf/metrics 的声明式语义登记，观测时不携带值
// （Prometheus 适配器绑定空 label 子指标；需要按平台拆分时用自定义 collector）。
func ensurePlatformMetrics() {
	platformMetricsOnce.Do(func() {
		caps := []string{capLogin, capPayment, capAudit, capWebhook}
		m := make(map[string]*capabilityMetrics, len(caps))
		for _, c := range caps {
			m[c] = &capabilityMetrics{
				calls: metrics.NewCounter(
					"tgf_platform_"+c+"_calls_total",
					"平台 "+c+" 能力累计调用次数", "platform"),
				fails: metrics.NewCounter(
					"tgf_platform_"+c+"_fail_total",
					"平台 "+c+" 能力累计失败次数", "platform"),
				latencyMs: metrics.NewHistogram(
					"tgf_platform_"+c+"_latency_ms",
					"平台 "+c+" 能力调用时延（毫秒）", platformLatencyBuckets, "platform"),
			}
		}
		platformCapMetrics = m
	})
}

// observePlatformCall 记录一次平台能力调用：时延 + 调用量 + 失败量。
func observePlatformCall(capability string, start time.Time, err error) {
	ensurePlatformMetrics()
	cm := platformCapMetrics[capability]
	if cm == nil {
		return
	}
	cm.latencyMs.Observe(float64(time.Since(start)) / float64(time.Millisecond))
	cm.calls.Inc()
	if err != nil {
		cm.fails.Inc()
	}
}

// resetMetricsForTest 重置指标对象的延迟初始化状态，使下一次观测重新绑定
// 当前 metrics.GetProvider()。仅测试串行使用，生产代码不应调用。
func resetMetricsForTest() {
	platformMetricsOnce = sync.Once{}
	platformCapMetrics = nil
}

// ---- 能力切面的 metrics 垫片 ----
//
// 每个垫片只包一个能力接口的方法；wrapWithMetrics 按被包 Provider 实现的
// 能力子集组合垫片——包装后的类型断言结果与原始 Provider 完全一致
//（只实现 Login 的平台包装后依旧探测不出 Payment 能力）。

// metricsBase 承载最小公分母 Provider（Name 透传）。
type metricsBase struct{ inner Provider }

func (m metricsBase) Name() string { return m.inner.Name() }

// metricsLogin 包装 LoginProvider.VerifyLogin。
type metricsLogin struct{ inner LoginProvider }

func (m metricsLogin) VerifyLogin(ctx context.Context, credential string) (identity *PlatformIdentity, err error) {
	start := time.Now()
	defer func() { observePlatformCall(capLogin, start, err) }()
	return m.inner.VerifyLogin(ctx, credential)
}

// metricsPayment 包装 PaymentProvider.VerifyPayment。
type metricsPayment struct{ inner PaymentProvider }

func (m metricsPayment) VerifyPayment(ctx context.Context, receipt PaymentReceipt) (result *PaymentResult, err error) {
	start := time.Now()
	defer func() { observePlatformCall(capPayment, start, err) }()
	return m.inner.VerifyPayment(ctx, receipt)
}

// metricsAudit 包装 ContentAuditProvider 的文本与图片审核（共用 audit 指标）。
type metricsAudit struct{ inner ContentAuditProvider }

func (m metricsAudit) AuditText(ctx context.Context, openID, text string) (result *AuditResult, err error) {
	start := time.Now()
	defer func() { observePlatformCall(capAudit, start, err) }()
	return m.inner.AuditText(ctx, openID, text)
}

func (m metricsAudit) AuditImage(ctx context.Context, openID string, image []byte) (result *AuditResult, err error) {
	start := time.Now()
	defer func() { observePlatformCall(capAudit, start, err) }()
	return m.inner.AuditImage(ctx, openID, image)
}

// metricsWebhook 包装 WebhookVerifier.VerifyWebhook。
type metricsWebhook struct{ inner WebhookVerifier }

func (m metricsWebhook) VerifyWebhook(r *http.Request) (err error) {
	start := time.Now()
	defer func() { observePlatformCall(capWebhook, start, err) }()
	return m.inner.VerifyWebhook(r)
}

// 能力位掩码（wrapWithMetrics 的组合索引）。
const (
	bitLogin = 1 << iota
	bitPayment
	bitAudit
	bitWebhook
)

// wrapWithMetrics 按 p 实现的能力子集组合 metrics 垫片，返回包装后的 Provider。
// Register 注册时自动调用——业务从注册表取到的能力实例每次调用都会记
// 成功/失败/时延，无需手工埋点。
//
// 实现说明：Go 无法动态组合接口方法集，这里用 16 种（2^4）显式组合保证
// “包装不放大也不缩小能力集”——类型断言语义与原始 Provider 严格一致。
func wrapWithMetrics(p Provider) Provider {
	lp, hasLogin := p.(LoginProvider)
	pp, hasPayment := p.(PaymentProvider)
	ap, hasAudit := p.(ContentAuditProvider)
	wv, hasWebhook := p.(WebhookVerifier)

	mask := 0
	if hasLogin {
		mask |= bitLogin
	}
	if hasPayment {
		mask |= bitPayment
	}
	if hasAudit {
		mask |= bitAudit
	}
	if hasWebhook {
		mask |= bitWebhook
	}

	base := metricsBase{inner: p}
	l := metricsLogin{inner: lp}
	pay := metricsPayment{inner: pp}
	a := metricsAudit{inner: ap}
	w := metricsWebhook{inner: wv}

	switch mask {
	case 0:
		return base
	case bitLogin:
		return struct {
			metricsBase
			metricsLogin
		}{base, l}
	case bitPayment:
		return struct {
			metricsBase
			metricsPayment
		}{base, pay}
	case bitLogin | bitPayment:
		return struct {
			metricsBase
			metricsLogin
			metricsPayment
		}{base, l, pay}
	case bitAudit:
		return struct {
			metricsBase
			metricsAudit
		}{base, a}
	case bitLogin | bitAudit:
		return struct {
			metricsBase
			metricsLogin
			metricsAudit
		}{base, l, a}
	case bitPayment | bitAudit:
		return struct {
			metricsBase
			metricsPayment
			metricsAudit
		}{base, pay, a}
	case bitLogin | bitPayment | bitAudit:
		return struct {
			metricsBase
			metricsLogin
			metricsPayment
			metricsAudit
		}{base, l, pay, a}
	case bitWebhook:
		return struct {
			metricsBase
			metricsWebhook
		}{base, w}
	case bitLogin | bitWebhook:
		return struct {
			metricsBase
			metricsLogin
			metricsWebhook
		}{base, l, w}
	case bitPayment | bitWebhook:
		return struct {
			metricsBase
			metricsPayment
			metricsWebhook
		}{base, pay, w}
	case bitLogin | bitPayment | bitWebhook:
		return struct {
			metricsBase
			metricsLogin
			metricsPayment
			metricsWebhook
		}{base, l, pay, w}
	case bitAudit | bitWebhook:
		return struct {
			metricsBase
			metricsAudit
			metricsWebhook
		}{base, a, w}
	case bitLogin | bitAudit | bitWebhook:
		return struct {
			metricsBase
			metricsLogin
			metricsAudit
			metricsWebhook
		}{base, l, a, w}
	case bitPayment | bitAudit | bitWebhook:
		return struct {
			metricsBase
			metricsPayment
			metricsAudit
			metricsWebhook
		}{base, pay, a, w}
	default: // bitLogin | bitPayment | bitAudit | bitWebhook：全能力
		return struct {
			metricsBase
			metricsLogin
			metricsPayment
			metricsAudit
			metricsWebhook
		}{base, l, pay, a, w}
	}
}
