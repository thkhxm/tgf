# tgf v2 API 速查（业务侧）

> 本表内容逐项核对自 tgf v2 主干与公开 v2.2.0 API。生成项目以 `tgfctl` 实际固定版本为准；遇到本表没覆盖的 API，
> 去读 tgf 源码与 `example/` 真实示例，**禁止凭记忆杜撰**。

## 1. Server builder（`rpc.NewRPCServer()...Run()`）

| 选项 | 作用 | 备注 |
|------|------|------|
| `WithSingleProcess()` | 单进程模式 = `WithStandalone()+WithInProcessDispatch()` | 零 Consul，SendRPCMessage 走 localDispatcher 直通 |
| `WithStandalone()` | 不挂 Consul + 不启动 RPC client watch | 纯 web/单测用 |
| `WithClientOnly()` | client-only：不建 rpcx server、不监听、不注册 service，但初始化 Consul discovery + RPC client | HTTP 接入层进程用；注意默认 Run() 即便无 service 也会监听并注册——必须显式调本方法才是 client-only |
| `WithService(svc IService)` | 装载业务 Module | 可多次 |
| `WithGatewayOptions(rpc.GatewayOptions{...})` | 统一网关入口 | 字段见 §2；不要与 Deprecated 的 WithGateway* 混用 |
| `WithGatewayKCP(kcpBuilder)` | 给已有 GateService 附加 KCP listener | 新代码优先用 GatewayOptions.KCP |
| `WithCache(tgf.CacheModuleRedis \| tgf.CacheModuleClose)` | 缓存档位 | Close=零外部依赖；Redis=网关路由表/业务缓存 |
| `WithHTTPService(web.Options{...})` | 装载 HTTP 服务（可多次挂多端口） | Addr 空读配置 HTTPPort(8090)；Backend 空自动注入 HTTP→RPC 桥 |
| `WithHTTPServiceConsul(opt web.Options, reg rpc.HTTPRegistration{...})` | HTTP 服务并注册进 Consul（health 路由 + TTL 续约） | reg 字段见 §3 |
| `WithMethodPolicy("mod.Method", rpc.MethodPolicy{Timeout, RateLimit})` | 方法级超时/限流（QPS） | HTTP 桥与客户端 RPC 同管道生效 |
| `WithDefaultRPCTimeout(d)` / `WithMethodTimeout(m, d)` | RPC 超时 | 默认 5s（RPCDefaultTimeoutMs） |
| `WithMetrics(p metrics.Provider)` | 注入 metrics | `metrics.NewMemoryProvider()`（内存/单测）、`metrics/prometheus.NewProvider()`（生产） |
| `WithTracer(t trace.Tracer)` | 注入 tracer | |
| `WithPlatform(p platform.Provider)` | 注册第三方平台 Provider（可多次注册多平台） | 重名/nil 启动期 fail-fast；注册自动套 metrics 包装；详见 §10 |
| `WithHealthCheck(interval)` | Consul TTL 心跳周期（建议 2~10s） | Consul 模式不调也默认开启（5s）；TTL=3×interval |
| `WithLoginTokenSecret(s)` | 登录 HMAC 密钥（优先于 env LoginTokenSecret） | |
| `WithLoginCheck(c ILoginCheck)` | 自定义登录校验（标准 JWT/平台 SDK） | 传 nil 恢复默认 HMAC |
| `WithoutLoginCheck()` | 显式关闭登录鉴权 | 仅内网联调；生产严禁 |
| `WithWhiteService("mod.Method")` | 登录前可调的白名单方法 | |
| `WithRandomServicePort(min, max int32)` | rpcx 端口随机化（同机多实例） | |
| `WithCustomServiceAddress()` | 注册地址用配置 ServiceAddress（NAT/多网卡） | 注意：该方法无返回值，不能链式续写 |
| `WithGameConfig(path)` | 加载 JSON 数值配置（component 包，含热更） | |
| `WithServerPool(maxWorkers, maxCapacity)` | rpcx 协程池 | |
| `WithShutdownDrainTimeout(d)` / `WithShutdownTimeout(d)` | 停机 drain 超时（默认 10s）/ 总看门狗 | |
| `WithProfileDebug()` | pprof/statsview | |

## 2. 网关 `rpc.GatewayOptions`

```go
rpc.GatewayOptions{
    TCPPort:   "8082",        // 空→默认 8230
    WSPath:    "/ws",         // 空→不启动 WebSocket
    WSTLSKey:  "key.pem",     // 与 WSTLSCert 同时非空→WSS
    WSTLSCert: "cert.pem",
    KCP:       rpc.NewKCPBuilder("8300").WithAEADKey(key32), // nil→不启动 KCP
}
```

- KCP AEAD：`WithAEADKey(key)` 必须 32 字节；nil/空=明文（仅开发/内网）；
  长度非法启动期 panic（fail-fast，防静默明文）。
- TCP/WS/KCP 可同时开，共享同一 GateService 与登录态。

**客户端可达方法签名**（网关帧直达；T 为 protobuf Message）：

```go
func (s *XxxService) Hello(ctx context.Context,
    args *rpc.Args[*pb.HelloReq], reply *rpc.Reply[*pb.HelloRes]) error {
    req := args.GetData()
    return reply.SetData(&pb.HelloRes{...})   // 错误码用 reply.SetCode(int32)
}
```

普通 struct 参数的方法只能被服务间 `SendRPCMessage` 调用，客户端帧路由不到。

## 3. HTTP（`tgf/web` 包）

```go
web.Options{
    Addr:   ":8090",                  // 空→读配置 HTTPPort
    Routes: func(r *web.Router) {
        r.GET("/health", web.Health())            // 或 web.HealthFunc(check)（自检失败 503 摘流量）
        r.GET("/users/{id}", h)                   // Go 1.22 ServeMux 语义，r.PathValue("id")
        r.POST("/users", h2)                      // 另有 PUT/DELETE/PATCH/Handle
        g := r.Group("/admin", web.Auth(web.BearerToken(func() string {
            return config.Current().Security.AdminToken  // 动态读，Reload 即热轮换
        })))                                      // 静态口令用 web.StaticBearerToken(s)
        g.GET("/stats", h3)
    },
    Limiter:         web.NewTokenBucketLimiter(1000), // 全局 QPS 令牌桶，超限 429
    Middlewares:     []web.Middleware{...},       // func(http.Handler) http.Handler，接在内置链后
    ShutdownTimeout: 10 * time.Second,
}
```

- 内置中间件链默认全开：Trace（`X-Trace-Id`，常量 `web.HeaderTraceID`）/
  AccessLog / Metrics / Recover；handler 内 `trace.TraceIDFromContext(r.Context())` 取链路 id。
- `web.Auth` 语义：未配置口令→503（fail-closed）；口令错/缺→401。
- **HTTP→RPC 桥**：handler 内 `backend, ok := web.BackendFromRequest(r)` →
  `backend.Invoke(r.Context(), "module", "Method", &args, &reply)`（args/reply 指针，
  签名语义同 rpcx 方法）。单进程命中 localDispatcher 直通，分布式走 rpcx+Consul，
  traceId / 方法策略 / metrics 全链路生效。失败统一回 502。

```go
rpc.HTTPRegistration{
    ServiceName: "myproj-web",    // 必填：Consul 检索键；空则跳过注册（启动 Error 日志）
    Address:     "",              // 空→自动探测 host + 实际监听端口（:0 也能注册真实值）
    HealthPath:  "",              // 空→ /health，自动挂载
    HealthCheck: func() error {...}, // 非 nil→自检失败 503 摘流量
    DisableHealthRoute: false,    // 已自行注册 /health 时置 true（否则路径冲突 panic）
}
```

## 4. Service 与 RPC

```go
// Module：内嵌获得 IService 默认实现；Name=Consul module 名
type UserService struct{ rpc.Module }
func NewUserService() *UserService {
    return &UserService{Module: rpc.Module{Name: "user", Version: "1.0"}}
}
func (s *UserService) Startup() (bool, error) { return true, nil } // 只有 (true,nil) 才进入发现/本地分发

// 服务间 RPC 契约（描述符集中放 internal/api）
var UserLogin = &rpc.ServiceAPI[*LoginReq, *LoginRes]{
    ModuleName: "user", Name: "Login", MessageType: "user.Login",
}
// 调用（单进程直通 / 分布式 rpcx，业务代码同一份）
res, err := rpc.SendRPCMessage(ctx, UserLogin.NewRPC(&LoginReq{...}))
```

ctx 内调用方身份用框架常量读（`tgf.ContextKeyUserId` 等），不要发明新 key。
`Startup` 返回 false 或 error 的 service 不对外服务，但仍由框架执行 `Destroy`；清理代码必须容忍部分初始化。
`Run()` 已统一处理 `SIGINT/SIGTERM`，业务 main 只等待返回 channel，禁止重复 `signal.Notify`。

## 5. 登录鉴权（默认开启，fail-closed）

```go
// 1) 配密钥：.env.<module> 里 LoginTokenSecret=<随机长串>（或 WithLoginTokenSecret）
// 2) 业务验证账号后签发：
token, err := rpc.GenerateLoginToken(userId, 12*time.Hour)
// 3) 客户端 gate.Login 帧带 token；服务侧辅助入口：
res, err := rpc.UserLoginWithToken(ctx, userId, token)
```

不配密钥：默认实现**拒绝所有登录**并打 Error 日志。自定义体系（标准 JWT/平台 SDK）
用 `WithLoginCheck(c)` 注入：`CheckLogin(token) (ok bool, uid string)`——uid 非空时
框架强制与请求 userId 一致（防冒充）。

## 6. 配置（`tgf/config` 包）

```go
cfg, err := config.Load()      // main 启动时显式加载（严格校验，fail-fast）
cfg := config.Current()        // 任意处读当前快照（atomic，无锁）
config.Reload()                // 热更：重读 .env 文件 + 原子替换（坏配置不吃进去）
config.OnReload(func(c *config.Config) {...})
```

常用键（`.env.<TGFMODULE>`，TGFMODULE=dev/test/release，默认 dev）：

| env 键 | 默认值 | 字段 |
|--------|--------|------|
| ServicePort / ServiceAddress | 8082 / 127.0.0.1 | `cfg.Service.*`（rpcx 内部端口） |
| ConsulAddress / ConsulPath | 127.0.0.1:8500 / /tgf | `cfg.Consul.*` |
| RedisAddr / RedisPassword / RedisDB / RedisCluster | 127.0.0.1:6379 / "" / 1 / false | `cfg.Redis.*` |
| MySqlAddr / MySqlPort / MySqlUser / MySqlPwd / MySqlDB | 127.0.0.1 / 3306 / root / 123456 / tgf | `cfg.MySQL.*` |
| HTTPPort | 8090 | `cfg.HTTP.Port` |
| LoginTokenSecret / ADMIN_TOKEN | ""（fail-closed） | `cfg.Security.*` |
| LogPath / LogLevel | ./log/tgf.log / debug | `cfg.Logger.*` |
| RPCDefaultTimeoutMs | 5000 | `cfg.RPC.*` |

## 7. 数据层（`tgf/db` 包）

```go
type UserData struct {
    db.Model
    Uid      string `orm:"pk"`     // 首字段 pk 标签=主键
    Nickname string
}
func (u UserData) GetTableName() string { return "t_user" }

cache := db.NewAutoCacheBuilder[string, *UserData]().
    WithMemCache(3600).                       // 内存层 TTL（秒）
    WithAutoCache("user", 24*time.Hour).      // Redis 层（key 前缀 + TTL）
    WithLongevityCache(5 * time.Second).      // MySQL write-behind 周期落库
    WithLongevityGroupSize(200).              // 每批条数
    WithLongevityRetry(3).                    // 失败重试
    WithLongevityFailureQueue(queue).         // 耗尽后入补偿队列
    New()
cache.Set(&UserData{...}, "u001"); v, err := cache.Get("u001")
// cache.Range(fn) / Remove(key) / Reset()

// 补偿队列：db.NewMemoryFailureQueue()（内存）/ FileFailureQueue（生产）
// 启动重放：db.ReplayFailureQueue(queue, func(p db.FailurePayload) error {...})
// 基础 Redis：db.Set/Get[T]/Del/PutMap/GetMap/AddListItem/GetList/FormatKey
// 分布式锁：lock, err := db.NewLock(key); defer db.UnLock(lock)
```

简化入口：`db.NewDefaultAutoCacheManager[K, V]("前缀")`（内存+Redis 默认配置）。

## 8. 日志 / metrics / trace

```go
log.InfoTag("user", "登录 userId=%v", uid)            // Sprintf 风格（tag 可被 LogIgnoredTags 过滤）
log.InfoTagW("user", "登录", zap.String("uid", uid))  // zap.Field 零分配（热路径）
log.WarnTag / log.ErrorTag / log.DebugTag 同形

m := metrics.NewMemoryProvider()                       // 或 prometheus.NewProvider()
trace.TraceIDFromContext(ctx)                          // HTTP/RPC 全链路同一 traceId
```

## 9. robot 联调客户端（`tgf/robot` 包）

```go
bot := robot.NewRobotTcp()            // 或 NewRobotWs("/ws") / NewRobotWss("/wss") / NewRobotKCP(aeadKey)
bot.RegisterCallbackMessage("game.GetRole", func(r robot.IRobot, data []byte) {
    res := &pb.GetRoleRes{}; proto.Unmarshal(data, res)   // data 是 proto 编码响应
})
bot.Connect("127.0.0.1:8082")         // 启动心跳 + 读 loop
bot.SendMessage("game", "GetRole", pbReq)  // pbReq 为 proto.Message
```

完整用法见 tgf 仓库 `example/robot_test/main.go`。

## 10. 平台 SDK 合约层（`tgf/platform` 包，v2.1 H1）

```go
// 注册（builder；重名 / nil / 空 Name 启动期 fail-fast 非零码退出）
rpc.NewRPCServer().
    WithPlatform(wechat.New(...)).   // 平台实现是独立 module：github.com/thkhxm/tgf-platform/*
    WithPlatform(tiktok.New(...)).   // 可多次注册多平台
    Run()

// 任意处按平台名 + 能力切面取用（平台实现了该能力才 ok=true）
lp, ok := platform.Login("wechat")    // (LoginProvider, bool)        VerifyLogin(ctx, credential)
pp, ok := platform.Payment("tiktok")  // (PaymentProvider, bool)      VerifyPayment(ctx, receipt)
ap, ok := platform.Audit("wechat")    // (ContentAuditProvider, bool) AuditText / AuditImage
wv, ok := platform.Webhook("tiktok")  // (WebhookVerifier, bool)      VerifyWebhook(r)
all := platform.List()                // 全量（按平台名字典序）

// 支付回调路由验签门禁（验签失败 401 / nil verifier 503 fail-closed；
// 防重放——时间窗 + nonce 去重——由 VerifyWebhook 实现内完成）
r.Group("/pay/tiktok", web.Middleware(platform.WebhookMiddleware(wv))).
    POST("/notify", notifyHandler)    // 与 web.Middleware 同构，platform 包不 import web

// 业务测试：可编程 Fake（全能力；未注入的字段函数走确定性默认返回）
f := &platform.Fake{
    FakeName: "wechat",
    VerifyLoginFunc: func(ctx context.Context, c string) (*platform.PlatformIdentity, error) {
        return &platform.PlatformIdentity{Platform: "wechat", OpenID: "u1"}, nil
    },
}
```

- **类型**：`PlatformIdentity`（OpenID/UnionID/SessionKey/Raw）、`PaymentReceipt` /
  `PaymentResult`（金额单位=最小货币单位「分/cents」）、`AuditResult`
  （Suggestion ∈ `SuggestionPass` / `SuggestionReview` / `SuggestionReject`）。
- **凭据**：走 `config.Current().Platform`（WechatAppID / WechatAppSecret / TiktokAppID /
  TiktokAppSecret / AppleTeamID / AppleKeyID / ApplePrivateKey / FacebookAppID /
  FacebookAppSecret），Secret 类启动日志自动脱敏；绝不 `os.Getenv` 直读。
- **metrics**：注册时自动包装，指标 `tgf_platform_{login,payment,audit,webhook}_{calls_total,fail_total,latency_ms}`。
- **平台实现纪律**（硬规则）：每个 endpoint 注释附官方文档链接+拉取日期；接入完成
  必须真凭据端到端验证——`go build` 通过不等于接通（见 tgf 仓库 `doc/platform-sdk-design.md` 第四节）。
