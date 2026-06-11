//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description H1：平台注册表——启动期写、运行期读，按平台名 + 能力切面取用
//2026/6/11
//***************************************************

package platform

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/thkhxm/tgf/v2/log"
	"go.uber.org/zap"
)

// 注册表存储。写入只发生在启动期（Register / rpc.Server.WithPlatform），
// 运行期全部是读——RWMutex 读锁开销可忽略（平台调用本身是出网请求）。
var (
	registryMu sync.RWMutex
	registry   = make(map[string]Provider)
)

// Register 把一个平台 Provider 注册进全局注册表。
//
// 行为：
//   - 注册时自动套 metrics 包装（wrapWithMetrics，能力子集原样保留），业务无感；
//   - nil Provider / 空 Name / 重名 → 返回 error 并打 Error 日志（不 panic）——
//     启动期 fail-fast 由 rpc.Server.WithPlatform 负责（注册失败非零码退出）；
//   - 并发安全：可与读取并发，多个 Register 串行化。
//
// 直接调本函数（不经 builder）适合纯库场景与单测；业务进程推荐统一走
// WithPlatform，享受与其它 builder 校验一致的 fail-fast 语义。
func Register(p Provider) error {
	if p == nil {
		err := errors.New("platform: Register 收到 nil Provider")
		log.ErrorTagW("platform", "平台注册失败", zap.Error(err))
		return err
	}
	name := strings.TrimSpace(p.Name())
	if name == "" {
		err := errors.New("platform: Provider.Name() 不能为空")
		log.ErrorTagW("platform", "平台注册失败", zap.Error(err))
		return err
	}
	wrapped := wrapWithMetrics(p)

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		err := fmt.Errorf("platform: 平台 %q 重复注册", name)
		log.ErrorTagW("platform", "平台注册失败", zap.String("platform", name), zap.Error(err))
		return err
	}
	registry[name] = wrapped
	log.InfoTagW("platform", "平台已注册",
		zap.String("platform", name),
		zap.String("capabilities", strings.Join(capabilitiesOf(p), ",")),
	)
	return nil
}

// Get 按平台名返回已注册的 Provider（带 metrics 包装）。
// 未注册时返回 (nil, false)。只需要 Name/存在性判断时用它；
// 要发起平台调用请用下面的能力取用函数。
func Get(name string) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Login 按平台名取登录校验能力。平台未注册或未实现 LoginProvider 时 ok=false。
func Login(name string) (LoginProvider, bool) {
	p, ok := Get(name)
	if !ok {
		return nil, false
	}
	lp, ok := p.(LoginProvider)
	return lp, ok
}

// Payment 按平台名取支付校验能力。平台未注册或未实现 PaymentProvider 时 ok=false。
func Payment(name string) (PaymentProvider, bool) {
	p, ok := Get(name)
	if !ok {
		return nil, false
	}
	pp, ok := p.(PaymentProvider)
	return pp, ok
}

// Audit 按平台名取内容安全审核能力。平台未注册或未实现 ContentAuditProvider 时 ok=false。
func Audit(name string) (ContentAuditProvider, bool) {
	p, ok := Get(name)
	if !ok {
		return nil, false
	}
	ap, ok := p.(ContentAuditProvider)
	return ap, ok
}

// Webhook 按平台名取回调验签能力。平台未注册或未实现 WebhookVerifier 时 ok=false。
func Webhook(name string) (WebhookVerifier, bool) {
	p, ok := Get(name)
	if !ok {
		return nil, false
	}
	wv, ok := p.(WebhookVerifier)
	return wv, ok
}

// List 返回全部已注册平台（带 metrics 包装），按平台名字典序排列（结果确定性）。
func List() []Provider {
	registryMu.RLock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	out := make([]Provider, 0, len(registry))
	sort.Strings(names)
	for _, n := range names {
		out = append(out, registry[n])
	}
	registryMu.RUnlock()
	return out
}

// capabilitiesOf 探测一个 Provider 实现了哪些能力切面（注册日志用）。
func capabilitiesOf(p Provider) []string {
	caps := make([]string, 0, 4)
	if _, ok := p.(LoginProvider); ok {
		caps = append(caps, capLogin)
	}
	if _, ok := p.(PaymentProvider); ok {
		caps = append(caps, capPayment)
	}
	if _, ok := p.(ContentAuditProvider); ok {
		caps = append(caps, capAudit)
	}
	if _, ok := p.(WebhookVerifier); ok {
		caps = append(caps, capWebhook)
	}
	if len(caps) == 0 {
		caps = append(caps, "none")
	}
	return caps
}

// resetForTest 清空注册表，测试之间隔离。生产代码不应调用。
func resetForTest() {
	registryMu.Lock()
	registry = make(map[string]Provider)
	registryMu.Unlock()
}
