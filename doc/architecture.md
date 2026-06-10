# tgf v2 架构

> 最后更新：2026-06-10（G 档：HTTP 一等公民）

## 总览

tgf 是一套基于 **rpcx + Consul + Redis + MySQL** 的 Go 分布式游戏服务器框架。
v2 相比 v1 补齐了稳定性、工程化、可观测性、API 一致性四块短板，但保留了 v1
的整体架构思路——对业务透明的 write-behind 缓存、跨节点的网关路由、单进程
多服务的 Module 聚合。

## 分层

```
 ┌────────────────────────────────────────────────────────────────┐
 │  业务侧 Module (IService + Hooks + StateHandler)              │
 └───────────────────────────┬────────────────────────────────────┘
                             │
 ┌───────────────────────────▼────────────────────────────────────┐
 │  Server builder (rpc.NewRPCServer)                            │
 │  - GatewayOptions  (C1)                                       │
 │  - WithStandalone  (C1)                                       │
 │  - WithHTTPService (G1)  ── HTTP 一等公民                      │
 │  - WithHTTPServiceConsul (G2) ── HTTP 服务注册进 Consul        │
 │  - WithClientOnly (G3)   ── 纯 client/web 进程(不伪装 RPC 节点) │
 │  - WithMethodPolicy (C6) / WithMetrics (B4) / WithTracer (B4) │
 │  - WithHealthCheck (A6)                                       │
 └───────────────────────────┬────────────────────────────────────┘
                             │
    ┌──────────────┬─────────┴──────────┬─────────────────────────┐
    │              │                    │                         │
 ┌──▼──────────┐ ┌─▼─────────────┐ ┌────▼──────────────────┐      │
 │ HTTP (web/) │ │  Gateway       │ │  RPC Server           │      │
 │ - Router(G1)│ │  - TCP / WS    │ │  - rpcx svc registry  │      │
 │ - 中间件链  │ │  - KCP (A8)    │ │  - Consul discovery   │      │
 │ - Backend桥 │─┼─→ IConn (A2)   │ │  - Method policy (C6) │      │
 │ - Shutdown  │ │  - login lock  │ │  - Metrics / Trace    │      │
 │   (G1)      │ │    (A3)        │ │                       │      │
 └──┬──────────┘ └─┬─────────────┘ └────┬──────────────────┘      │
    │              │                    │                         │
 ┌──▼──────────────▼────────────────────▼─────────────────────────▼┐
 │  Data layer                                                    │
 │  - autoCacheManager (write-behind, A1 + A1b FailureQueue)     │
 │  - game_config (C5 hot reload)                                │
 │  - config/ (C3 struct-tag driven)                             │
 └────────────────────────────────────────────────────────────────┘
```

> HTTP 层（`web/`）自包含：只依赖标准库 + log/metrics/trace 三个叶子包，
> **不 import rpc**（无 import 环）。`rpc.Server` 单向 import `web`，经
> `WithHTTPService` 把"调用后端 RPC 的能力"以 `web.Backend` 接口注入——HTTP
> handler 经桥即可调任意后端 service（图中 HTTP → Gateway/RPC 的虚线）。

## 关键包

| 包 | 职责 | v2 变更要点 |
|----|------|------------|
| `tgf` | 顶层入口、环境变量常量、错误类型 | ErrConnClosed / ErrConnSendTimeout / ErrUserNotFound 新增 |
| `tgf/web` | **v3 新增**：HTTP 服务核心（Server / Router / 中间件 / Backend 注入） | G1 HTTP 一等公民，自包含不 import rpc |
| `tgf/rpc` | 网关、IService、rpcx 封装 | A2 IConn / A3 login lock / A4 builder hooks / A7+C6 policy / A8 KCP / C1 unified gateway / C2 sub-interfaces / G1 WithHTTPService |
| `tgf/db` | 泛型 write-behind 自动缓存 | A1 可靠性 + B6 atomic state + C4 FailureQueue |
| `tgf/component` | 游戏配置热更 | C5 ReloadGameConf / OnReload / fsnotify |
| `tgf/config` | **v2 新增**：struct-tag 驱动的配置系统 | C3 首版 |
| `tgf/metrics` | **v2 新增**：可观测性指标接口 | B4 首版 |
| `tgf/trace` | **v2 新增**：分布式追踪接口 | B4 首版 |
| `tgf/log` | zap+lumberjack 封装 | B5 level+tag 前置过滤 + zap.Field 新 API |
| `tgf/util` | 通用工具 | A5 清理 + pool.GoE 新 API |

## 网关连接生命周期（A2 抽象）

```
accept TCP/WS/KCP ─▶ 包装为 IConn ─▶ handleConn(IConn)
                                        │
                                        ├─ reader goroutine (IConn.ReadFrame)
                                        ├─ logic goroutine (reqChan buffer=16)
                                        └─ writer goroutine (writeChan)
                                        │
                                        ▼
                                  Offline (sessionState CAS)
```

**IConn** 是 A2 引入的统一接口，TCP / WebSocket / KCP 三种传输的差异都被吸收
在 adapter 里。`handleConn` 只有一个入口，生命周期管理统一。

## 跨节点登录协调（A3）

```
Client ──▶ GateService.Login
              │
              ├─ 1. loginCoord.AcquireLoginLock(userId)   ← Redis redislock
              ├─ 2. loginCoord.GetGateOwner(userId)        ← Redis hashmap
              │      ┌─ 本节点 ──▶ 本地 Offline 旧会话
              │      └─ 远端节点 ──▶ KickRemoteOwner 发 RPC 踢
              ├─ 3. tcpService.DoLogin (本地 CAS state 机)
              ├─ 4. loginCoord.SetGateOwner(userId, self)
              └─ 5. loginCoord.ReleaseLoginLock()
```

详细路径见 `reports/A3-phase2-cross-node-login-lock.md`。

## RPC 调用策略（C6）

```
SendRPCMessage
    │
    ├─ observe start time       ← B4 metrics
    ├─ resolveMethodPolicy(m, n)
    │    │
    │    ├─ breaker.allow()     ← 断路器
    │    ├─ bucket.allow()      ← 令牌桶
    │    └─ semaphore acquire   ← 并发上限
    │
    ├─ xclient.Go(ctx, method, args, reply, done)
    │
    └─ select {
           case <-time.After(resolveRPCTimeout(m, n)):  ← A7 超时
           case <-call.Done:
       }
       ── defer observeRPCCall(..., err)  ← B4 metrics
       ── defer policyRelease(err)        ── 释放 + 断路器计数
```

## HTTP 一等公民（G1）

`tgf/web` 把 HTTP web 服务做成与 RPC 平级的一等公民。分层与防 import 环设计：

```
                       rpc.Server.WithHTTPService(web.Options)
                                    │ 注入 web.Backend（defaultWebBackend）
                                    ▼
 ┌──────────────────────────── tgf/web（自包含，不 import rpc）───────────────┐
 │  web.Server                                                              │
 │   ├─ http.Server 实例（ReadHeaderTimeout 等超时，防 slowloris）           │
 │   ├─ 中间件链（外→内）：Trace → AccessLog → Metrics → Recover            │
 │   │                     → RateLimit → Backend 注入 → 用户 Middlewares     │
 │   └─ Router（Go 1.22 ServeMux：method+path 模式 / 路径参数 / 分组）       │
 └──────────────────────────────────────────────────────────────────────────┘
                                    │ handler 内 web.BackendFromRequest(r)
                                    ▼
        backend.Invoke(ctx, module, method, &args, &reply)
                                    │
              ┌─────────────────────┴─────────────────────┐
       单进程：localDispatcher 直通                 分布式：SendRPCMessageByStr
       （命中本地 service，反射调用）                （经 Consul 服务发现跨节点）
              └─────────────────────┬─────────────────────┘
                         共用 E1 策略管道 + A7 超时 + E3 埋点
```

要点：

- **无 import 环**：`web` 只依赖标准库 + `log`/`metrics`/`trace` 三个叶子包；
  `rpc` 单向 import `web`，把后端调用能力以 `web.Backend` 接口（`Invoke(ctx,
  module, method, args, reply) error`）注入。`WithHTTPService` 装配时
  `Options.Backend` 留空即自动注入 `defaultWebBackend`。
- **traceId 全链路**：`web.Trace` 中间件注入的 traceId（入站 `X-Trace-Id` 复用，
  否则新生成）→ `newWebBackendContext` 写进 rpcx `ReqMetaData["TraceId"]` →
  后端 service 内 `trace.TraceIDFromContext(ctx)` 读到同一个 id。
- **优雅停机（共享 D3）**：HTTP 用独立 `http.Server` 实例，挂进 `Server.Destroy`
  序列——**先停 accept → drain in-flight HTTP（`http.Server.Shutdown` 带
  `ShutdownTimeout`）→ 再 drain RPC → 终末 flush**。
- **部署形态**：① 纯 web 进程（`WithStandalone` + `WithHTTPService`）；② 与游戏
  service 同进程（`WithService` + `WithHTTPService`，Backend 走 localDispatcher）；
  ③ client-only web 接入层（`WithClientOnly()` + `WithHTTPService`/`WithHTTPServiceConsul`，
  Backend 经服务发现跨节点调游戏服）。示例分别见 `example/http_rest/`、`example/http_rpc/`。
  > 注意：形态 ③ 必须显式 `WithClientOnly()`。仅"只 `WithHTTPService` 不 `WithService`"
  > **不是** client-only——默认 `Run()` 仍会创建 rpcx server、监听 `ServicePort`、
  > 在 discovery 非 nil 时注册进 Consul，即仍是一个 RPC 节点。`WithClientOnly()` 才
  > 不注册 service / 不监听 rpcx 端口 / 不进入 RPC 服务发现（与 `WithService` /
  > `WithGateway*` 互斥，`Run` 时 `validateClientOnly` fail-fast）。
- **HTTP 服务注册进 Consul（G2/G3）**：`WithHTTPServiceConsul(opt, reg HTTPRegistration)`
  = `WithHTTPService` + 把该 HTTP 服务注册进 Consul（自动挂 health 路由、TTL 续约或
  Consul 主动 HTTP 探测、`Destroy` 时先摘流量再 drain），使分布式 web 服务可被
  标准 Consul 生态发现与负载均衡；`HTTPRegistration` 字段含 `ServiceName` /
  `HealthPath` / `HealthCheck` / `DisableHealthRoute` / `UseHTTPCheck` 等。
  实现见 `tgf/rpc/http_consul.go`、`tgf/rpc/rpcserver_clientonly.go`。
- **配置项**：`HTTPPort`(8090) / `HTTPReadHeaderTimeoutSec`(5) /
  `HTTPShutdownTimeoutSec`(10)，登记于 `tgf/config.HTTPConfig` 与 `define.go`
  `Environment` 常量。

## write-behind 缓存（A1 + C4）

```
user.Push("uid")           ← 业务写入
    │
    ▼
autoCacheManager
    ├─ local cache (hashmap.Map)        ← 热存
    ├─ redis (optional)                  ← 二级缓存
    ├─ mysql via sqlBuilder              ← 冷存
    │
    ▼ timer.Tick (N seconds)
  toLongevity
    ├─ 收集 data_update 脏数据
    ├─ 按 group size 切批
    ├─ flushBatch per batch
    │     ├─ 成功 → removeState(data_update)
    │     └─ 失败 →
    │           ├─ 保留脏标志，下一轮再试
    │           └─ 序列化进 FailureQueue (C4/A1b)
    │
    └─ 启动阶段：db.ReplayFailureQueue(queue, flushFn)
```

## 可观测性（B4）

三个独立的接口包：

- `tgf/metrics`：Counter / Gauge / Histogram / Provider 接口 + NoopProvider
  默认实现。业务按需引入 Prometheus / OpenTelemetry adapter。
- `tgf/trace`：Span / Tracer 接口 + NoopTracer + trace id 生成。
- `tgf/log`：zap + tag 过滤 + level 前置检查（B5 优化）。

所有接口都默认零外部依赖。业务接入 Prometheus 的典型路径是写一个
`tgf/metrics/prom` subpackage 实现 `metrics.Provider` 接口然后调
`Server.WithMetrics(prom.NewProvider())`。

## 热更机制

| 对象 | 热更路径 | 触发方式 |
|------|---------|---------|
| 游戏配置（JSON） | `component.ReloadGameConf` | fsnotify watcher / 业务侧手动调用 |
| 环境配置（env） | `config.Reload` | 业务侧手动调用（显式热更 API） |
| RPC 策略 | `rpc.SetMethodPolicy` | 运行时任意调用 |
| 日志级别 | 暂无——zap 包初始化时一次性读 | 重启生效 |

> 说明：框架**未内置** SIGHUP 信号处理或 HTTP admin 的 reload 端点，
> `config.Reload` / `ReloadGameConf` 需业务侧自行决定触发时机（如自己挂信号
> handler 或 admin 路由后调用）。`StartConfigWatcher()` 提供的 fsnotify 目录监听
> 是唯一框架自带的自动触发器，仅作用于游戏配置 JSON 目录。
>
> E2 配置收敛后 `config.Reload()` 的完整语义：先重读 `.env.<module>` 文件
> （`tgf.InitConfig` 注入的 loader，文件值覆盖进程环境变量，使"改文件即热更"
> 成立；文件不存在时跳过），再重新解析并**原子替换**类型化快照与规范化字符串
> 快照——旧 `tgf.GetStrConfig` 与新 `config.Current()` 读同一份解析结果（bool
> 统一规范化为 `"1"/"0"`，`RedisCluster=true` 等宽松写法在新旧路径语义一致），
> 最后按注册顺序触发 `OnReload` 订阅者。解析失败时保留旧配置（坏值不会被热更
> 吃进去），仅返回 error。

## 单机模式 / 单进程模式

### WithStandalone（C1，不挂 Consul）

```go
rpc.NewRPCServer().
    WithStandalone().                          // = WithoutConsul().WithoutServiceClient()
    WithGatewayOptions(rpc.GatewayOptions{
        TCPPort: "8082",
    }).
    WithService(myService).
    Run()
```

单机模式下：
- 不装 Consul discovery hook
- 不启动 RPC client watch
- 依然可以完整用 Gateway / DB / Cache 等功能

适合本地开发、**单服务测试**、小规模部署。

### WithSingleProcess（进程内多 module）

如果一个进程里要启动**多个 module** 并且它们之间通过 `SendRPCMessage` 互相调用，
单纯 `WithStandalone()` 不够——rpcx client 没有 discovery 条目会直接失败。
使用 `WithSingleProcess()` 开启**进程内反射调用**：

```go
rpc.NewRPCServer().
    WithSingleProcess().                       // = WithStandalone() + WithInProcessDispatch()
    WithService(userService).
    WithService(shopService).
    WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).
    Run()
```

`userService` 在方法里调 `SendRPCMessage(ctx, Shop.BuyItem.NewRPC(req))`：
- `localDispatcher` 命中 `shopService` 已注册
- 直接反射调用 `shopService.BuyItem(ctx, req, reply)`
- 零网络开销，零 Consul 依赖
- 策略管道（限流/熔断/并发控制）依然生效——和分布式路径语义一致

单进程模式适合：
- **单元测试**：不需要 Consul/Redis 容器，完整跑 module 间通信
- **小型/原型项目**：一个进程装 5~10 个 module 够用
- **本地联调**：开发阶段所有 service 跑在一个进程里便于调试

不适合：
- 需要水平扩展的生产场景（这时应当走完整的 Consul + rpcx 网络路径）
- 动态增减 service 的场景（`registerLocalServices` 只在 `Run` 时一次性注册）

## client-only 纯 web 进程（G3）

无状态 REST 接入进程的形态：对外只提供 HTTP，handler 经 `web.Backend` 跨节点调
后端游戏 service，**自身不是一个 RPC 服务节点**。

### 为什么需要 `WithClientOnly()`

一个**反直觉但必须澄清**的点：**默认 `Run()` 即便不 `WithService` 任何 service，
仍会创建 rpcx server、监听 `ServicePort`（`rpc/rpcserver.go` 的 `net.Listen`）、
并在 discovery 非 nil 时把本进程注册进 Consul**——即它仍然是一个 RPC 节点。
"只 `WithHTTPService` 不 `WithService`" **不等于** client-only。

真正实现 client-only 语义的是 `WithClientOnly()`：

```go
func (s *rpc.Server) WithClientOnly() *rpc.Server
```

它开启后，`Run` 分流到 `runClientOnly`：

- **不**注册任何 service、**不**创建 rpcx server、**不**监听 rpcx 端口、**不**进入
  RPC 服务发现；
- **保留** Consul discovery + RPC client watch——`SendRPCMessage` / `ServiceAPI` /
  `web.Backend` 全部可用，可跨节点调后端；
- 可叠加 `WithHTTPService` / `WithHTTPServiceConsul` 构成纯 web 进程；
- 共享 D3 优雅停机序列。

约束（`Run` 时 `validateClientOnly` fail-fast，冲突即 `os.Exit(1)`）：与
`WithService` / `WithGateway*`（会装载 service）、`WithoutServiceClient`
（client-only 的意义就是要 RPC client）、`WithoutConsul`/`WithStandalone`/
`WithSingleProcess`（RPC client 依赖 Consul 定位后端）互斥。

### `WithHTTPServiceConsul` + `HTTPRegistration`

```go
func (s *rpc.Server) WithHTTPServiceConsul(opt web.Options, reg rpc.HTTPRegistration) *rpc.Server
```

`= WithHTTPService + 把这个 HTTP 服务注册进 Consul`。注册发生在 HTTP 监听就绪之后
（与 F2 "serve-ready-before-register" 同一时序），默认 TTL check + 框架续约
goroutine（容器/NAT 零配置可用），`Destroy` 时**先**反注册摘流量**再** drain。
`HTTPRegistration` 关键字段：

| 字段 | 说明 |
|------|------|
| `ServiceName` | Consul 逻辑名（发现/负载均衡检索键），必填；空则跳过注册。 |
| `Address` | 显式对外地址 `host:port`；空 → HTTP 实际监听地址（`:0` 也注册真实端口）。 |
| `HealthPath` | health 路径，空 → `/health`，自动挂路由（除非 `DisableHealthRoute`）。 |
| `HealthCheck func() error` | 业务自检，失败 503 摘流量；nil → 端口活性级探活。 |
| `DisableHealthRoute` | 已自挂 health 路由或不需要时置 true。 |
| `UseHTTPCheck` | true → Consul 主动 GET health；false（默认）→ TTL check。 |
| `Interval` / `Timeout` / `DeregisterCriticalAfter` | TTL 周期 / HTTP 探测超时 / critical 后摘除时长。 |
| `Tags` / `Meta` | 透传到 Consul service。 |

### 最小示例：client-only + HTTP + Consul 注册

```go
rpc.NewRPCServer().
    WithClientOnly().                       // 不伪装成 RPC 节点：不注册 service / 不监听 rpcx 端口
    WithHTTPServiceConsul(web.Options{       // 起 HTTP 服务并注册进 Consul（带 health 路由 + TTL 续约）
        Addr: ":8091",
        Routes: func(r *web.Router) {
            // web.RPC[Req,Res] 泛型 handler：HTTP body ⇄ 后端 module.method
            r.POST("/api/player", web.RPC[GetPlayerReq, GetPlayerRes]("player", "GetPlayer"))
        },
    }, rpc.HTTPRegistration{ServiceName: "player-web"}).
    Run()
```

此进程不出现在 RPC 服务发现里，但作为名为 `player-web` 的 **HTTP service** 出现在
Consul 中，可被 DNS / API / fabio / traefik 发现与负载均衡；其 handler 经
`web.Backend` → `SendRPCMessageByStr` 经服务发现跨节点调到游戏服的 `player.GetPlayer`。
若**不需要**注册进 Consul，把 `WithHTTPServiceConsul(opt, reg)` 换成
`WithHTTPService(opt)` 即可。完整可跑示例见 `example/http_rpc/`。

## 关键文件索引

- `tgf/web/web.go` — G1 HTTP Server（Options / 生命周期 / Backend 注入）
- `tgf/web/router.go` — G1 路由（Go 1.22 ServeMux + 分组 + 中间件链）
- `tgf/web/middleware.go` — G1 内置中间件（Trace/AccessLog/Metrics/Recover/RateLimit/Auth）
- `tgf/rpc/rpcserver.go` — Server builder、生命周期、Optional 管道、G1 WithHTTPService + defaultWebBackend
- `tgf/rpc/rpcserver_clientonly.go` — G3 WithClientOnly + validateClientOnly + runClientOnly
- `tgf/rpc/http_consul.go` — G2 WithHTTPServiceConsul + HTTPRegistration + HTTP 服务的 Consul 注册/续约/反注册
- `tgf/rpc/rpcserver_timeout.go` — A7 超时骨架
- `tgf/rpc/rpcserver_policy.go` — C6 策略化完整版
- `tgf/rpc/tcp.go` — 网关连接读写 / 推送 / 踢人
- `tgf/rpc/conn.go` — A2 IConn 抽象 + TCP/WS adapter
- `tgf/rpc/kcp.go` — A8 KCP listener + AEAD
- `tgf/rpc/gate.go` / `login_lock.go` — A3 登录协调
- `tgf/rpc/service.go` — IService / Module / 子接口 (C2)
- `tgf/rpc/metrics_hooks.go` — B4 埋点
- `tgf/rpc/internal/` — Consul discovery 单例 (A6)
- `tgf/db/manager.go` — 泛型自动缓存 (A1/B6)
- `tgf/db/failure_queue.go` — C4 补偿队列
- `tgf/config/config.go` — C3 新配置系统
- `tgf/component/game_config.go` — C5 热更
- `tgf/metrics/metrics.go` + `tgf/trace/trace.go` — B4 接口包
- `tgf/log/logger.go` — B5 日志热路径优化
