[![Go Report Card](https://goreportcard.com/badge/github.com/thkhxm/tgf)](https://goreportcard.com/report/github.com/thkhxm/tgf)
[![Go Version](https://img.shields.io/badge/go-1.26%2B-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

# tgf

**tgf** 是一套基于 Go 语言的**分布式游戏服务器框架**，同时把 **HTTP web 服务**
做成与 RPC 平级的一等公民。它专注于解决游戏 / web 业务开发中常见的稳定性、并发
与运维问题。v2 在 v1 基础上做了系统性的重构——从网关连接生命周期到数据落库
可靠性，从可观测性接口到 API 一致性，再到 HTTP 服务的路由 / 中间件 / 优雅停机，
都有明显的提升。

> 设计目标：让中小型团队与独立开发者**只关注业务逻辑**，不必处理连接风暴、
> 跨节点协调、配置热更、指标埋点这些底层细节。

tgf 的四类主要运行形态各有对应示例：

| 场景 | 形态 | 对应示例 |
|------|------|---------|
| **单进程游戏服** | 多 Module 进程内 RPC，零 Consul | [`example/single_process/`](example/single_process/) |
| **分布式游戏服** | 独立长连接网关（TCP/WS/KCP）+ 独立业务进程 | [`example/distributed_game/`](example/distributed_game/) |
| **HTTP REST** | 标准 REST API：路由 + 中间件 + 限流 + 鉴权 + 优雅停机 | [`example/http_rest/`](example/http_rest/) |
| **HTTP→RPC** | HTTP 接入层经 bridge 调后端 service；默认示例为单进程，多进程需 Consul | [`example/http_rpc/`](example/http_rpc/) |

TCP/WS/KCP、robot、write-behind、配置/游戏配置热更、日志、metrics/trace、
RPC 策略与 util 的完整“场景→example→证据→外部依赖”矩阵见
[`example/README.md`](example/README.md)。

## 目录

- [v2 特性亮点](#v2-特性亮点)
- [5 分钟快速上手](#5-分钟快速上手)
- [HTTP 一等公民](#http-一等公民g-档)
- [核心架构](#核心架构)
- [功能清单](#功能清单)
- [示例项目](#示例项目)
- [文档](#文档)
- [技术选型](#技术选型)
- [社区与交流](#社区与交流)

---

## v2 特性亮点

相对 v1，v2 的主要交付成果：

### 🛡️ 稳定性（A 档）

- **网关连接生命周期统一** — 新增 `IConn` 抽象层，TCP / WebSocket / KCP 三种传输共享同一套 `handleConn` 管道，消除 v1 两套并行实现带来的 goroutine 泄漏和状态不一致
- **跨节点登录原子化** — 基于 Redis 锁 + 定向踢远端 owner，替代 v1 的 `BorderRPCMessage` 广播方案
- **Write-behind 缓存可靠性** — `toLongevity` 重写，失败批次保留脏标志下轮补偿，配合 A1b 补偿队列跨重启恢复
- **KCP + AEAD 网关** — 第三种传输通道，ChaCha20-Poly1305 帧加密，复用 `IConn` 生命周期
- **RPC 超时策略化** — 支持按 `module.method` 覆盖全局默认超时

### 🔧 工程化（B 档）

- **构建基线** — Makefile / .golangci.yml / Dockerfile / GitHub Actions CI 全套
- **可观测性接口** — `tgf/metrics` 和 `tgf/trace` 两个新包，零外部依赖，默认 NoOp，业务按需写 Prometheus / OpenTelemetry adapter
- **日志热路径优化** — `log.*Tag` 系列加 level + tag 前置过滤，新增 `InfoTagW` 等 `zap.Field` 风格 API，避免 Sprintf 分配
- **依赖升级** — Go 1.26.4（最新稳定线）、go-sql-driver/mysql、ants、excelize、protobuf 等保守升级到主线

### 🌐 HTTP 一等公民（G 档）

- **`WithHTTPService(web.Options{...})`** — HTTP 与 RPC 平级的服务构建器，同进程共存、共享优雅停机；可多次调用挂多个端口
- **标准库路由** — 基于 Go 1.22 `net/http.ServeMux`（`"GET /users/{id}"` 模式 + 路径参数），零第三方 web 框架依赖
- **内置中间件链** — Trace（X-Trace-Id）/ AccessLog / Metrics / Recover / RateLimit / Auth，与 net/http 生态同构（`func(http.Handler) http.Handler`）
- **HTTP→RPC 桥** — handler 经注入的 `web.Backend` 调任意后端 service，单进程直通 / 分布式 rpcx 自动选路，traceId 从 HTTP 请求透传到后端 RPC
- **生产级停机** — `http.Server` 实例（带超时，防 slowloris）挂进 D3 停机序列，`Shutdown(ctx)` 带超时 drain in-flight 请求

### 🏗️ API / 架构演进（C 档）

- **GameConfig 热更** — `component.ReloadGameConf()` + `OnReload(fn)` + fsnotify 自动监听
- **配置系统升级** — 新 `tgf/config` 包，struct tag 驱动的反射加载器，新增字段只改一处
- **Server Builder 重构** — `WithGatewayOptions(...)` 合并 v1 四个平行 API，`WithStandalone()` 语义糖
- **IService 接口重塑** — 可选子接口 `IStatefulService` / `IUserLifecycleService`，Module 基类自动满足
- **DB 补偿队列** — `FailureQueue` 接口 + NoopImpl / MemoryImpl / FileImpl 三种实现
- **RPC 策略化完整版** — `MethodPolicy{Timeout, MaxConcurrency, RateLimit, CircuitBreaker}`，集成 metrics 拒绝路径计数
- **单进程模式（C8）** — `WithSingleProcess()` 一键开关，多 Module 在同一进程通过反射直通通信，**零 Consul 依赖**，适合单元测试和小型部署

### 📊 验证边界

- `go test -count=1 ./example/...`：编译所有示例，并运行无外部依赖的
  handler/service 与场景治理测试。
- `go vet ./example/...`：对同一示例边界执行静态检查。
- Consul、Redis、MySQL、真实端口和跨进程链路属于 integration 证据；
  只有在显式启动并观测它们后才能声称通过，默认测试不静默跳过也不伪装。

---

## 5 分钟快速上手

### 1. 安装

要求 Go **1.26+**（go.mod 声明 `go 1.26.0` / `toolchain go1.26.4`）。

tgf 自 v2.0.0 起 module path 带 `/v2` 主版本后缀，新项目直接获取
最新已发布版：

```bash
go get github.com/thkhxm/tgf/v2@latest
```

需要可复现构建时，将首次解析后写入 `go.mod` 的确切版本（例如
`v2.x.y`）固定在构建配置中，不要让 CI/发布流水线反复重新解析
`@latest`。

import 路径相应带 `/v2`，例如：

```go
import (
    "github.com/thkhxm/tgf/v2/rpc"
    "github.com/thkhxm/tgf/v2/web"
)
```

> **下游业务工程不需要任何 `replace` 块。** tgf 依赖的 rpcx / rpcx-consul fork 也已
> 迁移为带 `/v2` 后缀的自有 module path（`github.com/thkhxm/rpcx/v2` /
> `github.com/thkhxm/rpcx-consul/v2`，当前 tag `v2.0.5` / `v2.0.3`），由 tgf 的
> `go.mod` 直接 `require` 引入并随之自动拉取——业务方既不必 `require` 它们，也不必
> 写 `replace`。

#### 从源码构建（贡献者）

本仓库与 fork 一起放在一个 Go **workspace**（仓库外的 `go.work` 同时 `use`
`tgf/`、`rpcx/`、`rpcx-consul/` 三个目录）时，workspace 会优先使用本地 fork，适合
框架组联调。`tgf/go.mod` 不包含本地 `replace`；脱离 workspace（例如发布隔离验证使用
`GOWORK=off`）时，会严格解析其中声明的公开 tag，避免源码联调通过但公开依赖图不可用。

### 2. 配置与凭据（重要）

tgf 按 `TGFMODULE` 环境变量加载 `.env.<module>` 文件（缺省 `dev`），里面是 Consul /
Redis / MySQL 的地址与口令。**真实凭据绝不入库**：

- 仓库只提供占位值模板 `.env.example` / `.env.test.example` / `.env.release.example`（被跟踪，安全）。
- 复制模板为对应的 `.env.dev` / `.env.test` / `.env.release` 并填入本地/生产真实值：

  ```bash
  cp .env.example .env.dev          # 然后改 RedisPassword / MySqlPwd 等真实口令
  ```

- `.env`、`.env.dev`、`.env.test`、`.env.release` 等已被 `.gitignore` 忽略，`git add .` 不会把它们提交。
- 生产环境推荐通过容器编排 / 密钥管理（Vault、K8s Secret 等）注入这些值，而不是落盘提交。

### 3. Hello, tgf（单进程模式，不需要 Consul / Redis / MySQL）

```go
package main

import (
    "fmt"
    "time"

    "github.com/thkhxm/tgf/v2/rpc"
    "golang.org/x/net/context"
)

// --- 定义一个 Service ---

type HelloService struct {
    rpc.Module
}

func (s *HelloService) Startup() (bool, error) { return true, nil }

type HelloReq struct{ Name string }
type HelloRes struct{ Greeting string }

func (s *HelloService) Greet(ctx context.Context, req *HelloReq, reply *HelloRes) error {
    reply.Greeting = "你好, " + req.Name
    return nil
}

var GreetAPI = &rpc.ServiceAPI[*HelloReq, *HelloRes]{
    ModuleName: "hello", Name: "Greet", MessageType: "hello.Greet",
}

// --- 启动 + 调用 ---

func main() {
    // 1. 启动单进程 server
    rpc.NewRPCServer().
        WithSingleProcess().                                       // 不挂 Consul
        WithService(&HelloService{Module: rpc.Module{Name: "hello"}}).
        WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).
        Run()
    time.Sleep(200 * time.Millisecond)

    // 2. 在任何地方通过 SendRPCMessage 调用（进程内直通）
    res, err := rpc.SendRPCMessage(context.Background(),
        GreetAPI.NewRPC(&HelloReq{Name: "世界"}))
    if err != nil {
        fmt.Printf("失败: %v\n", err)
        return
    }
    fmt.Println(res.Greeting) // → 你好, 世界
}
```

**跑起来**：

```bash
go run main.go
# 输出: 你好, 世界
```

没有 Consul、没有 Redis、没有 MySQL——`WithSingleProcess()` 让框架跑在零依赖模式。
从单进程原型平滑迁移到分布式部署只需要去掉这一行调用。

更多场景见 [`example/`](example/) 目录下的示例项目。

---

## HTTP 一等公民（G 档）

tgf 把 HTTP web 服务做成与 RPC 平级的一等公民：用 `WithHTTPService` 即可在同一个
进程里起一个标准 HTTP 服务，与 RPC 服务**共享生命周期与优雅停机**。核心实现放在
自包含的 `tgf/web` 包（只依赖标准库 + log/metrics/trace 三个叶子包，**不 import
rpc**，无 import 环）；`rpc.Server` 单向 import `web`，把"调用后端 RPC 的能力"以
`web.Backend` 接口注入进来。

### 最小用法

```go
import (
    "net/http"
    "github.com/thkhxm/tgf/v2/rpc"
    "github.com/thkhxm/tgf/v2/web"
)

rpc.NewRPCServer().
    WithStandalone().                       // 纯 web 进程：不挂 Consul
    WithHTTPService(web.Options{
        Addr: ":8090",                      // 省略则读配置 HTTPPort（默认 8090）
        Routes: func(r *web.Router) {
            r.GET("/health", func(w http.ResponseWriter, _ *http.Request) {
                _, _ = w.Write([]byte("ok"))
            })
            r.GET("/users/{id}", func(w http.ResponseWriter, req *http.Request) {
                _, _ = w.Write([]byte(req.PathValue("id")))   // Go 1.22 路径参数
            })
            // 鉴权分组：/admin/* 全部要求 Bearer 口令（fail-closed）
            admin := r.Group("/admin", web.Auth(web.StaticBearerToken("s3cr3t")))
            admin.GET("/stats", statsHandler)
        },
        Limiter: web.NewTokenBucketLimiter(1000),   // 全局 1000 QPS（E1 同款语义）
    }).
    Run()                                   // 启动监听；收到信号后 Destroy 优雅 drain
```

> 完整可跑示例：[`example/http_rest/`](example/http_rest/)。

### 路由与中间件

- **路由**：基于 Go 1.22 `net/http.ServeMux`。模式 `"GET /users/{id}"`（method + 路径参数），
  handler 内 `req.PathValue("id")` 取参；`r.Group(prefix, mws...)` 派生带前缀的分组，
  `r.Use(mws...)` 只影响**之后**注册的路由。便捷方法 `GET/POST/PUT/DELETE/PATCH`。
- **内置中间件链**（外 → 内）：`Trace → AccessLog → Metrics → Recover → RateLimit →
  Backend 注入 → 用户 Middlewares → 路由`。可经 `DisableTrace/DisableAccessLog/
  DisableMetrics/DisableRecover` 单独关闭。
- **中间件形状**：`type Middleware func(http.Handler) http.Handler`，与 net/http 生态
  完全同构——任何第三方中间件直接可用。内置构造器：`Trace()` / `AccessLog()` /
  `Metrics()` / `Recover()` / `RateLimit(Limiter)` / `Auth(AuthFunc)`。
- **鉴权**：`web.Auth(web.StaticBearerToken(token))` 或 `web.BearerToken(provider)`
  （支持热轮换）。比对走 `subtle.ConstantTimeCompare` 防时序侧信道；未配置口令 →
  503（fail-closed），带错/缺口令 → 401。

### HTTP→RPC 桥（分布式 web 服务）

HTTP handler 经框架注入的 `web.Backend` 调用任意后端 service，**不必裸写 net/http**
（若进程本身想"不伪装成 RPC 节点"，用 `WithClientOnly()`，见下文）：

```go
func getPlayer(w http.ResponseWriter, r *http.Request) {
    backend, _ := web.BackendFromRequest(r)          // 框架自动注入的默认后端
    args := GetPlayerReq{PlayerId: r.PathValue("id")}
    var reply GetPlayerRes
    if err := backend.Invoke(r.Context(), "player", "GetPlayer", &args, &reply); err != nil {
        http.Error(w, err.Error(), http.StatusBadGateway)
        return
    }
    // 写 reply ...
}
```

- `WithHTTPService` 装配时 `Options.Backend` 留空 → 框架自动注入默认后端
  （`defaultWebBackend`）：单进程命中 `localDispatcher` 走进程内直通，否则走
  `SendRPCMessageByStr` 分布式路径。两条路径业务代码一样，且共用 **E1 策略管道
  （限流/熔断）+ A7 超时 + E3 埋点**。
- **traceId 全链路**：`web.Trace` 中间件注入的 traceId（入站 `X-Trace-Id` 复用，
  否则新生成）会被写进 rpcx `ReqMetaData`，后端 service 内
  `trace.TraceIDFromContext(ctx)` 读到的是同一个 id。
- 业务也可注入自己的 `web.Backend`（如更丰富的 HTTP→RPC 编解码桥）替换默认实现。

> 完整可跑示例（含单进程 / 多进程两种部署）：[`example/http_rpc/`](example/http_rpc/)。

### 与 RPC 共存 / client-only / Consul 注册

- **与 RPC 共存**：同一个 `rpc.NewRPCServer()` 既 `WithService(...)` 注册游戏 service，
  又 `WithHTTPService(...)` 起 HTTP 服务，二者同进程、同一条 D3 优雅停机序列。
- **client-only web 接入层**：HTTP 进程用 `WithClientOnly()` + `WithHTTPService(...)`
  （或 `WithHTTPServiceConsul(...)`）。`WithClientOnly()` 才是"不把自己伪装成 RPC
  节点"的开关：**不注册任何 service、不创建 rpcx server、不监听 rpcx 端口、不进入
  RPC 服务发现**，但照常初始化 Consul discovery + RPC client，所以 `Backend.Invoke`
  经服务发现跨节点调游戏服。
  > ⚠️ 常见误解：**默认 `Run()` 即便不 `WithService` 任何 service，仍会创建 rpcx
  > server、监听 `ServicePort`、并在 discovery 非 nil 时注册进 Consul**——那仍是一个
  > RPC 节点，**不是** client-only。client-only 语义只有显式调用 `WithClientOnly()`
  > 才成立（它与 `WithService` / `WithGateway*` / `WithoutServiceClient` /
  > `WithoutConsul` 互斥，`Run` 时 `validateClientOnly` fail-fast）。

  从单进程平滑迁移到多进程分布式只改 server 构建那几行，handler 一字不改
  （见 `example/http_rpc/`）。
- **HTTP 服务注册进 Consul**：`WithHTTPServiceConsul(opt, reg)` = `WithHTTPService` +
  把这个 HTTP 服务注册进 Consul（自动挂 health 路由、TTL 续约、`Destroy` 时摘除），
  让"分布式 web 服务"可被标准 Consul 生态（DNS / API / fabio / traefik）发现与
  负载均衡。详见下方「[client-only + HTTP + Consul 注册：纯 web 接入进程](#client-only--http--consul-注册纯-web-接入进程)」。
- **优雅停机**：HTTP 用独立 `http.Server` 实例（带 `ReadHeaderTimeout` 等超时，防
  slowloris），挂进框架 `Destroy` 链；停机时**先停 accept → drain in-flight HTTP
  请求（`http.Server.Shutdown` 带 `ShutdownTimeout`）→ 再 drain RPC → 终末 flush**。
- **脱离 rpc 单独用**：`web.NewServer(opts).Start()` / `Shutdown(ctx)` 可不依赖
  `rpc.Server` 独立起一个 HTTP 服务（此时 `Backend` 需自行注入或不调后端）。

### HTTP 一等公民 · Server builder API 清单

| API | 作用 |
|-----|------|
| `WithHTTPService(opt web.Options) *Server` | 装载一个 HTTP 服务（**不**注册 Consul）。可多次调用起多个端口。 |
| `WithHTTPServiceConsul(opt web.Options, reg HTTPRegistration) *Server` | 装载 HTTP 服务**并注册进 Consul**（自动挂 health 路由 + TTL 续约 + `Destroy` 摘除），使分布式 web 服务可被发现/负载均衡。 |
| `WithClientOnly() *Server` | 开启 **client-only** 模式：不注册 service、不创建 rpcx server、不监听 rpcx 端口，仅保留 RPC client（可调后端）+ HTTP 服务 + HTTP 的 Consul 注册。与 `WithService` / `WithGateway*` 互斥。 |

`HTTPRegistration`（`WithHTTPServiceConsul` 的第二参数）关键字段：

| 字段 | 说明 |
|------|------|
| `ServiceName string` | Consul service 逻辑名（发现/负载均衡检索键）。必填，为空则跳过注册（Error 日志提示）。 |
| `Address string` | 显式对外可达地址 `host:port`。空 → 取 HTTP 实际监听地址（`:0` 随机端口也注册真实端口）。 |
| `HealthPath string` | health endpoint 路径。空 → `/health`。 |
| `HealthCheck func() error` | 业务自检（如 Redis/MySQL ping），失败时 health 返 503 摘流量；nil → 端口活性级探活。 |
| `DisableHealthRoute bool` | 已自行注册 health 路由或不需要 endpoint 时置 true。 |
| `UseHTTPCheck bool` | true → Consul agent 主动 GET health；false（默认）→ TTL check（框架续约 goroutine 维持，容器/NAT 零配置可用）。 |
| `Interval` / `Timeout` / `DeregisterCriticalAfter` | TTL 续约周期 / HTTP 探测超时 / critical 后自动摘除时长。 |
| `Tags []string` / `Meta map[string]string` | 透传到 Consul service。 |

#### client-only + HTTP + Consul 注册：纯 web 接入进程

一个无状态 REST 接入进程：自身**不**注册成 RPC 节点（`WithClientOnly`），只对外提供
HTTP，并把这个 HTTP 服务注册进 Consul（可被发现/负载均衡），handler 经 `web.Backend`
跨节点调后端游戏 service：

```go
package main

import (
    "github.com/thkhxm/tgf/v2/rpc"
    "github.com/thkhxm/tgf/v2/web"
)

type GetPlayerReq struct{ PlayerId string }
type GetPlayerRes struct {
    PlayerId string
    Level    int
}

func main() {
    rpc.NewRPCServer().
        // client-only：不伪装成 RPC 节点（不注册 service、不监听 rpcx 端口），
        // 但初始化 RPC client，可经服务发现调后端。
        WithClientOnly().
        // 装载 HTTP 服务并把它注册进 Consul（带 health 路由 + TTL 续约）。
        WithHTTPServiceConsul(web.Options{
            Addr: ":8091",
            Routes: func(r *web.Router) {
                // web.RPC[Req,Res] 泛型 handler：HTTP body ⇄ 后端 module.method，
                // 内部经 web.Backend 跨节点调 player.GetPlayer。
                r.POST("/api/player", web.RPC[GetPlayerReq, GetPlayerRes]("player", "GetPlayer"))
            },
        }, rpc.HTTPRegistration{
            ServiceName: "player-web", // Consul 逻辑名
            // 其余字段取默认：TTL check、/health 路由、本机出口 IP + 实际端口注册。
        }).
        Run()
}
```

要点：

- **client-only 不是"少 WithService"**：默认 `Run()` 不 `WithService` 任何 service
  时仍会创建 rpcx server、监听 `ServicePort` 并注册进 Consul——必须显式
  `WithClientOnly()` 才真正不伪装成 RPC 节点。
- 若**不需要**把 HTTP 服务注册进 Consul（如前面有外部 LB / 网关），把
  `WithHTTPServiceConsul(opt, reg)` 换成 `WithHTTPService(opt)` 即可，其余不变。
- handler 也可不用泛型 `web.RPC`，而在普通 `http.HandlerFunc` 内
  `web.BackendFromRequest(r)` 取 `Backend` 后 `backend.Invoke(ctx, module, method, &args, &reply)`
  手动调用（见 `example/http_rpc/`）。

### 配置项

| 配置项（env） | 默认值 | 说明 |
|---------------|--------|------|
| `HTTPPort` | `8090` | `WithHTTPService` 未显式指定 `Addr` 时的监听端口 |
| `HTTPReadHeaderTimeoutSec` | `5` | 读请求头超时（秒，防 slowloris） |
| `HTTPShutdownTimeoutSec` | `10` | 优雅停机 drain 超时（秒） |

登记于 `tgf/config.HTTPConfig` 与 `define.go` 的 `Environment` 常量；
`Options` 的零值字段在 `Run` 时按这些配置项与 `web` 包默认值填充。

---

## 核心架构

```
┌───────────────────────────────────────────────────────────────┐
│  业务侧 Module（IService + Hooks + StateHandler）             │
└───────────────────────────┬───────────────────────────────────┘
                            │
┌───────────────────────────▼───────────────────────────────────┐
│  Server Builder (rpc.NewRPCServer)                           │
│  ├── WithSingleProcess / WithStandalone (C1/C8)              │
│  ├── WithGatewayOptions (C1) / WithGatewayKCP (A8)           │
│  ├── WithHTTPService (G1) ── HTTP 一等公民                    │
│  ├── WithMethodPolicy (C6) / WithMetrics (B4) / WithTracer   │
│  └── WithHealthCheck (A6)                                    │
└───────────────────────────┬───────────────────────────────────┘
                            │
   ┌───────────────┬────────┴───────────────┬───────────────────┐
   │               │                        │                   │
┌──▼───────────┐ ┌─▼──────────────┐  ┌──────▼─────────────────┐ │
│ HTTP (web/)  │ │  Gateway        │  │  RPC Server            │ │
│ ├ Router(G1) │ │  ├ TCP / WS/WSS │  │  ├ rpcx service registry│ │
│ ├ 中间件链   │ │  ├ KCP+AEAD(A8) │  │  ├ Consul discovery     │ │
│ ├ Backend桥  │─┼─→ IConn (A2)    │  │  ├ localDispatcher (C8) │ │
│ └ Shutdown   │ │  └ 登录锁 (A3)   │  │  └ MethodPolicy 管道(C6)│ │
└──┬───────────┘ └─┬──────────────┘  └──────┬─────────────────┘ │
   │               │                        │                   │
┌──▼───────────────▼────────────────────────▼───────────────────┐
│  Data Layer                                                   │
│  ├── autoCacheManager (write-behind, A1 + A1b FailureQueue)  │
│  ├── game_config (C5 hot reload + fsnotify)                  │
│  └── config/ (C3 struct-tag 驱动)                              │
└───────────────────────────────────────────────────────────────┘
```

> HTTP（`web/`）是自包含层（不 import rpc，无 import 环）；`rpc.Server` 经
> `WithHTTPService` 把后端调用能力以 `web.Backend` 注入，HTTP handler 即可经桥
> 调任意后端 service（图中 HTTP → Gateway/RPC 的虚线）。

完整架构说明见 [`doc/architecture.md`](doc/architecture.md)。

---

## 功能清单

### HTTP web 服务（G 档）

| 能力 | API | 说明 |
|------|-----|------|
| HTTP 服务构建器 | `Server.WithHTTPService(web.Options{...})` | 与 RPC 同进程共存，可多次调用挂多端口 |
| 路由 | `web.Router` `GET/POST/PUT/DELETE/PATCH` / `Handle("GET /p/{x}", h)` | Go 1.22 ServeMux，路径参数 `req.PathValue` |
| 路由分组 | `r.Group(prefix, mws...)` / `r.Use(mws...)` | 前缀 + 分组中间件 |
| 内置中间件 | `Trace / AccessLog / Metrics / Recover / RateLimit / Auth` | 链顺序外→内，可单独 Disable |
| 限流 | `web.NewTokenBucketLimiter(qps)` | E1 同款令牌桶，超限 429 |
| 鉴权 | `web.Auth(web.StaticBearerToken(t))` / `web.BearerToken(provider)` | constant-time + fail-closed |
| HTTP→RPC 桥 | `web.BackendFromRequest(r).Invoke(ctx, module, method, &args, &reply)` | 单进程直通 / 分布式选路，traceId 透传 |
| 优雅停机 | `Run()` + 框架 `Destroy` 链 | 独立 `http.Server` + `Shutdown(ctx)` 带超时 drain |
| 脱离 rpc 单用 | `web.NewServer(opts).Start()` / `Shutdown(ctx)` | 不依赖 rpc.Server 独立起 HTTP |

### 网关与连接

| 能力 | API | 说明 |
|------|-----|------|
| TCP / WS / WSS / KCP 网关 | `WithGatewayOptions(GatewayOptions{TCPPort, WSPath, WSTLSKey, WSTLSCert, KCP})` | 统一入口，按需组合 |
| KCP + AEAD | `NewKCPBuilder(port).WithAEADKey(key)` | ChaCha20-Poly1305 帧加密 |
| 踢人 / 重复登录 | `loginCoord` 接口 | Redis 锁 + 定向踢远端 |
| 健康心跳 | `WithHealthCheck(interval)` | F2 已接真实 Consul TTL check（Consul 开启且注册成功时默认 5s 自动启用，TTL=3×interval，节点宕机自动摘除） |
| 单进程模式 | `WithSingleProcess()` | 反射直通，零 Consul |

### 数据缓存

| 能力 | API | 说明 |
|------|-----|------|
| 泛型自动缓存 | `NewAutoCacheBuilder[Key, Val]()` | builder 配置内存 / Redis / MySQL 三级 |
| 快捷入口 | `NewDefaultAutoCacheManager` / `NewLongevityAutoCacheManager` | 常用配置预设 |
| 基础 KV / Map / List | `db.Get / Set / GetMap / PutMap / GetList / AddListItem` | Redis 操作 |
| 分布式锁 | `db.NewLock(key)` + `db.UnLock(lock)` | 基于 bsm/redislock |
| 补偿队列（A1b） | `FailureQueue` + `ReplayFailureQueue` | 落库失败跨重启恢复 |

### RPC 调用

| 能力 | API | 说明 |
|------|-----|------|
| 同步调用 | `SendRPCMessage[Req, Res](ctx, api)` | 带超时 + 策略检查 |
| 单向调用 | `SendNoReplyRPCMessage` | 后台执行 |
| 异步调用 | `SendAsyncRPCMessage` | 返回 Call |
| 指定地址 | `SendNoReplyRPCMessageByAddress` | 跨节点定向 |
| 方法策略 | `WithMethodPolicy / SetMethodPolicy` | Timeout + 限流 + 熔断 + 并发 |

### 可观测性

| 能力 | API | 说明 |
|------|-----|------|
| 指标 | `metrics.NewCounter / NewGauge / NewHistogram` | 接口 + NoOp 默认 |
| Provider 切换 | `Server.WithMetrics(p)` | 业务注入 Prometheus adapter |
| 追踪 | `trace.StartSpan(ctx, name)` | 接口 + NoOp 默认 |
| Tracer 切换 | `Server.WithTracer(t)` | 业务注入 OpenTelemetry adapter |
| 框架内建埋点 | `tgf_gate_connections` / `tgf_rpc_latency_ms` 等 7 个 | 自动触发 |

### 日志

| 能力 | API | 说明 |
|------|-----|------|
| Sprintf 风格 | `log.InfoTag(tag, msg, params...)` | v1 兼容 |
| zap.Field 风格（v2 推荐） | `log.InfoTagW(tag, msg, zap.Field...)` | 零分配热路径 |
| Tag 过滤 | `log.CheckLogTag(tag)` | 由 `LogIgnoredTags` 环境变量控制 |
| 分类日志 | `log.Game / DB / Service` | 写到独立文件 |

### 配置

| 能力 | API | 说明 |
|------|-----|------|
| 环境变量加载 | `config.Load()` | struct tag 驱动 |
| 当前快照 | `config.Current()` | 原子读 |
| 热更 | `config.Reload()` + `OnReload(fn)` | 业务侧手动调用，或经 admin 控制面 `POST /config/reload` 触发（`ServeAdmin` 启用，需 ADMIN_TOKEN）；Reload 会先重读 `.env.<module>` 文件（文件值覆盖进程 env），旧 `tgf.GetStrConfig` 同步读到新值 |
| 游戏配置 | `component.GetGameConf[Val](id)` | 泛型查询 |
| 游戏配置热更 | `component.ReloadGameConf()` + `StartConfigWatcher()` | 手动调用 / fsnotify 目录监听 |

---

## 示例项目

[`example/`](example/) 目录下有 12 个可运行的示例目录。详细证据等级
和外部依赖状态以 [`example/README.md`](example/README.md) 的场景矩阵为准：

| 目录 | 演示内容 |
|------|---------|
| [`distributed_game/`](example/distributed_game/) | 真实双进程长连接网关 + 业务服（Consul/Redis integration） |
| [`http_rest/`](example/http_rest/) | **纯 REST API**（WithHTTPService + 路由/中间件/限流/鉴权/优雅停机）——常规 http web 服务 |
| [`http_rpc/`](example/http_rpc/) | **REST + 调游戏服 RPC**（默认单进程桥；多进程是外部 integration） |
| [`single_process/`](example/single_process/) | 单进程多 Module + 跨 module RPC + 策略 + metrics |
| [`robot_test/`](example/robot_test/) | 本机 TCP + WS + KCP robot 请求/回包自测 |
| [`db_cache/`](example/db_cache/) | AutoCacheBuilder + Redis KV/Map/List + 分布式锁 + 补偿队列 |
| [`log_usage/`](example/log_usage/) | Sprintf vs zap.Field 风格 + Tag 过滤 + 分类日志 |
| [`config_reload/`](example/config_reload/) | struct tag 加载 + Reload + OnReload + 类型校验 |
| [`metrics_trace/`](example/metrics_trace/) | Counter / Gauge / Histogram + Span + TraceID |
| [`game_config/`](example/game_config/) | JSON 游戏配置 + ReloadGameConf + fsnotify watcher |
| [`rpc_policy/`](example/rpc_policy/) | 限流 / 熔断 / 并发控制 三场景 |
| [`util_tools/`](example/util_tools/) | 协程池 + Snowflake + 随机数 + 类型转换 |

**直接运行**：

```bash
cd tgf/example/single_process
go run .
```

不含外部依赖的示例可直接 `go run .`。`distributed_game` 需 Consul/Redis；
`db_cache` 只在显式设置 `TGF_EXAMPLE_EXTERNAL=1` 时尝试 Redis/MySQL，否则明确报告未执行。

---

## 文档

| 文档 | 说明 |
|------|------|
| [`doc/architecture.md`](doc/architecture.md) | v2 完整架构图 + 关键包索引 + 流程说明 |
| [`doc/migration-v1-to-v2.md`](doc/migration-v1-to-v2.md) | v1 → v2 迁移指南 + 升级 checklist |
| [`doc/observability.md`](doc/observability.md) | Prometheus / OpenTelemetry / Loki 接入指南 |
| [`CHANGELOG.md`](CHANGELOG.md) | 完整变更日志 |

### 外部链接

- **API 参考**：[pkg.go.dev/github.com/thkhxm/tgf/v2](https://pkg.go.dev/github.com/thkhxm/tgf/v2)
- **项目地址**：[github.com/thkhxm/tgf](https://github.com/thkhxm/tgf)
- **项目文档**：[thkhxm.github.io/tgf_writerside](https://thkhxm.github.io/tgf_writerside/starter-topic.html)
- **国内文档镜像**：[tgf.yamigame.net:8080](http://tgf.yamigame.net:8080/)
- **示例项目**：[github.com/thkhxm/tgf-tutorial](https://github.com/thkhxm/tgf-tutorial)
- **知乎博客**：[tim-30-83](https://www.zhihu.com/people/tim-30-83/posts)
- **CSDN 专栏**：[tgf 专栏](https://blog.csdn.net/thkhxm/category_12520142.html)
- **B 站教程**：[space.bilibili.com/64497732](https://space.bilibili.com/64497732/channel/seriesdetail?sid=3815364)

---

## 技术选型

**Go 工具链**：Go 1.26+（go.mod 声明 `go 1.26.0` / `toolchain go1.26.4`）

| 类别 | 库 | 版本 | 用途 |
|------|----|------|------|
| RPC | [thkhxm/rpcx](https://github.com/thkhxm/rpcx) | fork 自 smallnest/rpcx，已迁移为自有 module path 直接 require | 底层 RPC 引擎 |
| 服务发现 | [thkhxm/rpcx-consul](https://github.com/thkhxm/rpcx-consul) | fork 自 rpcxio/rpcx-consul，已迁移为自有 module path 直接 require | Consul 适配 |
| 缓存 | [go-redis/v9](https://github.com/redis/go-redis) | v9.7.0 | Redis 客户端 |
| 分布式锁 | [bsm/redislock](https://github.com/bsm/redislock) | v0.9.4 | 基于 Redis 的锁 |
| 数据库 | [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql) | v1.9.3 | MySQL 驱动 |
| 日志 | [zap](https://go.uber.org/zap) | v1.27.1 | 结构化日志 |
| 日志切割 | [lumberjack.v2](https://gopkg.in/natefinch/lumberjack.v2) | v2.2.1 | 滚动归档 |
| 并发 | [ants/v2](https://github.com/panjf2000/ants) | v2.12.0 | 协程池 |
| JSON | [sonic](https://github.com/bytedance/sonic) | v1.15.0 | 高性能 JSON |
| 线程安全集合 | [cornelk/hashmap](https://github.com/cornelk/hashmap) | v1.0.8 | 无锁 map |
| ID 生成 | [bwmarrin/snowflake](https://github.com/bwmarrin/snowflake) | v0.3.0 | Snowflake |
| 一致性哈希 | [edwingeng/doublejump](https://github.com/edwingeng/doublejump) | v1.0.1 | jump consistent hash |
| KCP（A8 新增） | [xtaci/kcp-go](https://github.com/xtaci/kcp-go) | v5.4.20 | 可靠 UDP 传输 |
| 文件监听（C5 新增） | [fsnotify](https://github.com/fsnotify/fsnotify) | v1.9.0 | 配置目录 watcher |
| 环境变量 | [godotenv](https://github.com/joho/godotenv) | v1.5.1 | .env 文件加载 |
| WebSocket | [gorilla/websocket](https://github.com/gorilla/websocket) | v1.5.3 | WS 服务端/客户端 |
| Excel | [excelize/v2](https://github.com/qax-os/excelize) | v2.10.1 | Excel 读写 |

---

## 社区与交流

- **QQ 交流群**：7400585
- **Issues**：欢迎通过 GitHub Issues 提 bug 报告和功能建议
- **PR**：fork → 新建 `feature/xxx` 分支 → 提交 PR

### 贡献指南

1. 阅读 [`doc/architecture.md`](doc/architecture.md) 了解架构
2. 查看 [`doc/migration-v1-to-v2.md`](doc/migration-v1-to-v2.md) 了解 API 约定
3. 运行 `make test` 或 `go test -race ./...` 确认测试通过
4. 新增功能请同时补单元测试和示例

### 路线图

- ✅ v2-alpha：A / B / C 档落地（稳定性修复 + 工程化 + API 演进）
- ✅ **v2 D 档：止血与发布可用**（已随 v2.0.0 发布）——消灭 P0（串包、单进程网关、
  优雅停机、下游可消费、凭据卫生、登录鉴权地基），让框架"对外存在"
- ✅ **v2 E 档：接线收尾**（已随 v2.0.0 发布）——策略管道全覆盖、配置系统收敛、
  可观测性落地、数据层故障路径
- ✅ **v2 F 档：生产化地基**（已随 v2.0.0 发布）——fork 治理（rpcx / rpcx-consul 迁移
  为带 `/v2` 后缀的自有 module path）、Consul TTL check、会话与踢人收尾、过载保护
- ✅ **v2 G 档：HTTP 一等公民**（已随 v2.0.0 发布）——`WithHTTPService` + 路由/中间件/
  限流/鉴权 + HTTP→RPC 桥 + 共享 D3 优雅停机，覆盖 REST 与 HTTP→RPC 两种 HTTP 形态；
  单进程和真实分布式游戏服分别由独立示例承载
- 📅 更远期（v2.x 后续小版本）：
  - DB 层真正的分库分表（sqlBuilder 重构）
  - OpenTelemetry / Prometheus adapter 官方 subpackage

---

## License

MIT License — 见 [LICENSE](LICENSE)。

---

*tgf v2.2.0 · 最后更新 2026-07-24*
