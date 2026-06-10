# http_rpc —— REST + 调用游戏服 RPC（分布式 web 服务接游戏后端）

演示"分布式 web 服务"形态：一个对外的 HTTP/REST 入口，把请求经 **HTTP→RPC 桥**
转发给后端游戏 service（rpcx）。HTTP handler 不直接碰业务数据，而是通过框架注入
的 `web.Backend` 调用任意后端 `module.method`——与游戏客户端走同一套 service
实现、同一条策略管道（限流/熔断）、同一份可观测埋点，且 traceId 从 HTTP 请求
一路透传到后端 RPC。

## 运行

```bash
cd tgf/example/http_rpc
go run .
```

本示例是**单进程混合部署**：HTTP 入口 + 游戏 service 同进程，用
`WithSingleProcess()` 让 `Backend.Invoke` 走 `localDispatcher` 进程内直通，
因此不需要 Consul / Redis 即可直接跑。

## 试一试

```bash
# GET → 桥 → player.GetPlayer
curl -i http://127.0.0.1:8091/players/player_001

# POST → 桥 → player.GiveGift
curl -i -X POST http://127.0.0.1:8091/players/player_001/gift -d '{"giftId":"welcome"}'

# 链路追踪：入站 X-Trace-Id 会透传到后端 service，并回写响应头
curl -i -H "X-Trace-Id: my-trace-1" http://127.0.0.1:8091/players/player_001
```

服务端日志里 `player` tag 的 "查询玩家" 行会打印同一个 `traceId=my-trace-1`，
证明 HTTP 请求 → 后端 RPC 全链路同一个 traceId。

## HTTP→RPC 桥怎么接线

1. `WithHTTPService(web.Options{...})` 装配时 **`Options.Backend` 留空** →
   框架自动注入默认后端（`defaultWebBackend`）：单进程命中 `localDispatcher`
   走进程内直通，否则走 `SendRPCMessageByStr` 分布式路径。两条路径业务代码一样。
2. handler 内 `web.BackendFromRequest(req)` 取出 `Backend`。
3. `backend.Invoke(ctx, "player", "GetPlayer", &args, &reply)` 调后端——
   `args`/`reply` 为指针，语义与 rpcx service 方法签名 `(ctx, *Req, *Res) error` 一致。
4. 用请求的 `r.Context()` 当 ctx：框架把 HTTP 链路 traceId 透传进 rpcx
   `ReqMetaData`，并对调用应用 E1 策略管道 + A7 超时 + E3 埋点。

## 拆成多进程（真正的分布式部署）

把单进程示例拆成「HTTP 接入进程 + 若干游戏服进程」只需两步：

**游戏服进程**（注册进 Consul）：

```go
rpc.NewRPCServer().
    WithService(NewPlayerService()).   // 去掉 WithSingleProcess
    WithMethodPolicy("player.GiveGift", rpc.MethodPolicy{Timeout: 2 * time.Second, RateLimit: 500}).
    Run()                              // 默认挂 Consul + 注册 player 服务
```

**HTTP 接入进程**（client-only web 层，不注册任何 service）：

```go
rpc.NewRPCServer().
    // WithClientOnly() 才是"不把自己伪装成 RPC 节点"的开关：不注册 service、
    // 不创建 rpcx server、不监听 rpcx 端口、不进入 RPC 服务发现，但照常初始化
    // Consul discovery + RPC client，所以下面的 Backend.Invoke 仍可跨节点调后端。
    WithClientOnly().
    // WithHTTPServiceConsul 装载 HTTP 服务并把这个 web 接入层本身注册进 Consul
    // （自动挂 health 路由 + TTL 续约 + Destroy 时摘除），使其可被发现/负载均衡。
    WithHTTPServiceConsul(web.Options{
        Addr:   ":8091",
        Routes: registerRoutes,         // 同本示例的 handler，一字不改
    }, rpc.HTTPRegistration{
        ServiceName: "player-web",       // Consul 逻辑名（发现/负载均衡的检索键）
    }).
    Run()
```

> ⚠️ 注意：**默认 Run() 即便不 `WithService` 任何 service，仍会创建 rpcx server、
> 监听 `ServicePort`，并在 discovery 非 nil 时把自己注册进 Consul**——那仍然是一个
> RPC 节点，**不是** client-only。真正实现 client-only 语义（不伪装成 RPC 节点）的是
> `WithClientOnly()`，它与 `WithService` / `WithGateway*` 互斥（`validateClientOnly`
> 在 `Run` 时 fail-fast）。若只想起 HTTP 服务而不注册进 Consul，用 `WithHTTPService`
> 替代 `WithHTTPServiceConsul` 即可（其余不变）。

此时 `backend.Invoke(ctx, "player", "GetPlayer", ...)` 不命中本地
`localDispatcher`，自动走 `SendRPCMessageByStr` → 经 Consul 服务发现找到游戏服
进程 → rpcx 跨节点调用。**handler 代码完全不变**，单进程到分布式的迁移只改
server 构建那几行。

> 多进程跑需要本机/集群有 Consul（`.env.<module>` 里配 `ConsulAddress`）。
> 该 client-only 入口也是审计 5 建议补的「不必伪装成 RPC 节点即可调后端服务」的落地形态。

## 端口与配置

`Addr: ":8091"` 显式写死；省略则读配置项 `HTTPPort`（默认 8090）。
