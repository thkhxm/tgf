# 第三方平台 SDK 接入设计（TikTok / 微信 / Facebook / Apple …）

> 状态：H1 已实现（`tgf/platform` 合约包 + 注册表 + Fake + `WithPlatform` +
> `PlatformConfig` 配置组 + Webhook 中间件桥，2026-06-11）；H2 起的平台实现待排期。
> 决策日期：2026-06-11。

## 一、结论

**不采用 Go 原生 plugin（.so 动态加载）**，采用「**核心合约接口 + 平台独立子模块 + 双部署形态**」：

| 层 | 位置 | 内容 |
|---|---|---|
| 合约层 | `tgf/platform`（主仓新包，零第三方依赖） | 按能力切面拆分的小接口 + 通用类型 + 注册表 + fake 实现 |
| 实现层 | `github.com/thkhxm/tgf-platform/{wechat,tiktok,apple,facebook}`（独立仓库，**每个平台独立 go module**） | 各平台 SDK 封装，实现其支持的合约子集 |
| 接入层 | 业务项目 | `WithPlatform(wechat.New(cfg))` 进程内注册，或部署独立 `platform` service 经 RPC 调用 |

### 为什么排除 Go plugin

- Windows 不支持（本项目开发环境即 Windows）；
- 宿主与插件必须用完全相同的工具链与依赖版本编译，版本漂移即崩溃；
- 社区事实弃用；
- 游戏服不存在"运行时热插拔 SDK"的真实需求——接入哪些平台编译期已确定。

### 为什么平台实现要独立 go module

微信/TikTok 等官方或社区 SDK 依赖树庞杂。独立 module 保证：**不接微信的项目，
二进制里没有微信的一行代码、`go.mod` 里没有它的任何依赖**。v3 刚完成依赖治理
（fork /v2 化、保守升级），不允许平台 SDK 把依赖卫生拖回去。

## 二、合约接口草案（`tgf/platform`）

按能力切面拆小接口，平台模块实现其支持的子集，框架用类型断言探测能力
（与 C2 可选子接口 `IStatefulService`/`IUserLifecycleService` 同一模式）：

```go
package platform

// Provider 是所有平台实现的最小公分母。
type Provider interface {
	// Name 返回平台标识："wechat" / "tiktok" / "apple" / "facebook" / ...
	Name() string
}

// PlatformIdentity 是平台登录校验成功后的标准化身份。
type PlatformIdentity struct {
	Platform   string // 平台标识
	OpenID     string // 平台内唯一 id（微信 openid / TikTok open_id / Apple sub / FB user id）
	UnionID    string // 跨应用统一 id（平台支持时）
	SessionKey string // 会话密钥（平台返回时；用于后续解密手机号等敏感数据）
	Raw        map[string]string // 平台原始字段透传（不强行抽象）
}

// LoginProvider 平台登录凭据校验。
// 微信 jscode2session / TikTok code2session / Apple Sign-In identityToken / FB access_token 校验。
type LoginProvider interface {
	Provider
	VerifyLogin(ctx context.Context, credential string) (*PlatformIdentity, error)
}

// PaymentProvider 支付校验与发货确认。
// Apple App Store Server API / 微信米大师回调 / TikTok 虚拟支付 / Google Play Developer API。
type PaymentProvider interface {
	Provider
	VerifyPayment(ctx context.Context, receipt PaymentReceipt) (*PaymentResult, error)
}

// ContentAuditProvider 内容安全审核（小游戏平台对 UGC 的强制要求）。
type ContentAuditProvider interface {
	Provider
	AuditText(ctx context.Context, openID, text string) (*AuditResult, error)
	AuditImage(ctx context.Context, openID string, image []byte) (*AuditResult, error)
}

// WebhookVerifier 平台服务端回调（支付通知等）的验签 + 防重放。
// 设计为可直接包装成 web.Middleware 挂在回调路由上。
type WebhookVerifier interface {
	Provider
	VerifyWebhook(r *http.Request) error
}
```

注册与读取（builder 风格，与全框架一致）：

```go
// 业务进程内注册（小型部署）
rpc.NewRPCServer().
	WithPlatform(wechat.New(wechat.Config{ /* AppID/Secret 从 config.Current().Platform 读 */ })).
	WithPlatform(tiktok.New(...)).
	...

// 任意处按平台名 + 能力取用
lp, ok := platform.Login("wechat")     // (LoginProvider, bool)
pp, ok := platform.Payment("tiktok")   // (PaymentProvider, bool)
```

## 三、与既有机制的对接点（全部复用，零新地基）

| 需求 | 复用机制 |
|---|---|
| 平台登录 → 框架登录 | D7 的 `WithLoginCheck(ILoginCheck)`：客户端把平台 credential 放进 `LoginReq.Token`，校验器调 `platform.VerifyLogin` 换 openid、绑定/创建 userId。 |
| 支付回调 webhook | G 档 `WithHTTPService`：回调就是一条 HTTP 路由；`WebhookVerifier` 包装成 `web.Middleware`（验签 + 防重放）挂在该路由上。 |
| AppID / AppSecret | E2 配置系统：新增 `PlatformConfig` 配置组（struct tag），凭据登记 `sensitiveEnvKeys` 自动脱敏，绝不 `os.Getenv` 直读、绝不入库。 |
| 多游戏共享 / 密钥集中管控 | 部署独立 `platform` service（普通 `IService` 模块），其它服务经 `SendRPCMessage` 调它——密钥只存在这一个进程。脚手架 skill 增加该场景模板。 |
| 可观测 | E3：每个平台调用打 metrics（成功/失败/时延，label=platform+capability）+ traceId 贯通。 |
| 测试 | 合约包自带 `platform.Fake`（可编程返回值），业务测试不打真实平台 API。 |

## 四、工程纪律（硬规则，源自全局规则 §2.8 的实战教训）

接入任何平台 API 前：

1. **先查官网最新文档，不许凭记忆写**。endpoint / 鉴权头格式 / 参数单位必须以官方文档为准。
2. **每个 endpoint 在代码注释附文档链接 + 拉取日期**，例如：
   ```go
   // TikTok code2session
   // 文档：https://developer.open-tiktok.com/...（2026-06-11 拉取）
   // 注意：access_token 必须用「用户 OAuth token」，不是 app client_credentials token。
   ```
3. **接入完成必须真凭据端到端验证一次**——`go build` 通过不等于接通。
4. 调试持续报"data invalid"类错误时，优先怀疑 endpoint 选错 / SKU 不匹配 / 参数单位错，
   而不是反复调 payload（历史教训：TikTok progress 0-1 被当 0-100、IAP 用错 token 类型）。

## 五、落地排期建议（v2.1 / H 档）

1. H1：`tgf/platform` 合约包 + 注册表 + Fake + `WithPlatform` + `PlatformConfig` 配置组 + web 中间件桥。
2. H2：首个平台打样——建议 **TikTok**（近期项目密钥/文档现成），覆盖 Login + Payment + Webhook 三能力，
   按本文档第四节纪律执行，产出可复制的"平台实现模板"。
3. H3：微信（Login + 内容安全 + 米大师支付）；之后 Apple / Facebook 按业务需求驱动。
4. 同步：脚手架 skill 问答树增加"需要接入哪些平台"问题位；`doc/coding-style.md` 增补平台接入规范一节。
