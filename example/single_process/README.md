# tgf v2 单进程模式示例

本目录演示了 tgf v2 的**单进程多 Module** 场景：一个进程里同时跑 UserService 和 ShopService，
Module 间通过 `rpc.SendRPCMessage` 通信，**零 Consul 依赖、零网络开销**。

## 运行

```bash
cd tgf/example/single_process
go run .
```

启动后会自动模拟一次 `user.Login` → 跨 module 调 `shop.GiveGift` → `shop.BuyItem` 的完整流程，
控制台输出调用结果和 metrics 快照。

## 演示的 v2 特性

| 特性 | 代码位置 | 说明 |
|------|---------|------|
| `WithSingleProcess()` | main.go | 一键开启：不挂 Consul + 进程内 RPC 直通 |
| `SendRPCMessage` 跨 module | UserService.Login | Login 内部调 ShopGiveGift，走 localDispatcher |
| `WithMethodPolicy` | main.go | 给 `shop.GiveGift` 加 100 QPS 限流 |
| `WithGatewayOptions` | main.go | C1 统一网关入口 |
| `config.Load()` | main.go | C3 struct-tag 配置系统 |
| `metrics.NewMemoryProvider` | main.go | B4 内存 metrics，演示埋点可观测 |
| `log.InfoTagW` | 各 service | B5 zap.Field 零分配日志 |
| `WithHealthCheck` | main.go | A6 健康心跳骨架 |

## 架构

```
main.go
  │
  ├─ UserService (module="user")
  │    └─ Login(req) → SendRPCMessage → ShopService.GiveGift
  │
  ├─ ShopService (module="shop")
  │    ├─ GiveGift(req)
  │    └─ BuyItem(req)
  │
  └─ GateService (TCP :8082)
       └─ 客户端连接入口
```

**调用路径**：
1. main goroutine 调 `rpc.SendRPCMessage(ctx, UserLogin.NewRPC(req))`
2. SendRPCMessage 查 `localDispatchEnabled` → true
3. 查 `localDispatcher.Lookup("user")` → UserService 已注册
4. 反射调 `UserService.Login(ctx, req, reply)`
5. Login 内部再调 `rpc.SendRPCMessage(ctx, ShopGiveGift.NewRPC(giftReq))`
6. 同上路径反射调 `ShopService.GiveGift`

全程**零网络**、**零 Consul**。策略管道（C6 限流/熔断）和 metrics 埋点（B4）一直生效。

## 网关 + 单进程（v3 / D4）

v3 之前**单进程 + 网关组合是不可用的**：客户端发来的第一条 Logic 帧会在
`doLogic → sendMessage → getRPCClient` 处对 nil discovery panic（P0-2）。
v3 起 `sendMessage` 在单进程模式下走 localDispatcher 本地直通，真实 TCP/WS/KCP
客户端消息可以端到端到达本地注册的 service：

1. 客户端连 `:8082` 发 Logic 帧（`module.method` + pb 负载）；
2. 网关 `doLogic` 检查登录态/白名单（`WithWhiteService("module.method")` 与分布式语义一致）；
3. 命中 localDispatcher → 反射直通本地 service → 响应帧原路回写。

注意：**网关可达的方法**参数必须是 `rpc.Args[T]` / `rpc.Reply[T]` 形式
（与分布式模式的 rpcx 序列化约定一致）。本示例的 `user.Login` 使用普通 struct
参数，属于"进程内 RPC"风格，只能被 `SendRPCMessage` 调用、不能被网关帧直达。
端到端回归见 `tgf/rpc/gateway_local_dispatch_test.go` 的
`TestSingleProcessGateway_EndToEnd`。

## 登录鉴权（v3 / D7，默认开启）

v3 起框架默认开启登录凭据校验（fail-closed）：`gate.Login` 要求 `LoginReq.Token`，
由默认 HMAC token 实现（或 `WithLoginCheck` 注入的自定义校验器）验证，
**伪造 userId / 过期 token / 无 token 一律拒绝**。

- 配置密钥：`.env.<module>` 加 `LoginTokenSecret=<随机长字符串>`，或代码
  `WithLoginTokenSecret(...)`；
- 业务登录服务验证账号后用 `rpc.GenerateLoginToken(userId, ttl)` 签发 token，
  客户端登录时带上，业务侧调 `rpc.UserLoginWithToken(ctx, userId, token)`；
- 本地开发/内网如确不需要鉴权，**显式** `WithoutLoginCheck()` 恢复旧行为
  （本示例未走 `gate.Login`，故无需配置）。

## 生产迁移

从单进程模式迁移到分布式部署只需要两步：

1. 去掉 `WithSingleProcess()`，改为默认模式（自动挂 Consul）
2. 把 UserService 和 ShopService 拆成独立进程

**业务代码（ServiceAPI 定义、RPC handler 签名、SendRPCMessage 调用点）完全不变。**
