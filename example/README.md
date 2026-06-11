# tgf v2 示例

每个子目录是一个可独立运行的示例，演示 tgf v2 的不同模块。

```bash
cd tgf/example/<目录>
go run .
```

## 示例列表

| 目录 | 对应包 | 演示内容 |
|------|--------|---------|
| `single_process/` | `rpc` | 单进程多 Module（WithSingleProcess + 跨 module RPC + 策略 + metrics） |
| `http_rest/` | `web` / `rpc` | **纯 REST API**（WithHTTPService + 路由/中间件/限流/鉴权/优雅停机）——常规 http web 服务 |
| `http_rpc/` | `web` / `rpc` | **REST + 调游戏服 RPC**（HTTP→RPC 桥 + traceId 全链路 + 单进程/多进程部署）——分布式 web 服务接游戏后端 |
| `robot_test/` | `rpc` | WS + KCP robot 自测（登录 + 多人移动同步，压测 QPS） |
| `db_cache/` | `db` | AutoCacheBuilder 三种缓存 + Redis 操作 + 分布式锁 + 补偿队列 |
| `log_usage/` | `log` | Sprintf 风格 vs zap.Field 风格 + Tag 过滤 + 特殊日志 |
| `config_reload/` | `config` | struct tag 配置加载 + Reload 热更 + OnReload + 类型校验 |
| `metrics_trace/` | `metrics` / `trace` | Counter / Gauge / Histogram + StartSpan + TraceID 透传 |
| `game_config/` | `component` | JSON 游戏配置 + GetGameConf + 热更 + fsnotify watcher |
| `rpc_policy/` | `rpc` | 限流 / 熔断 / 并发控制三种策略场景 |
| `util_tools/` | `util` | 协程池 Go/GoE + Snowflake ID + 随机数 + IP + 类型转换 |

> **三种场景对应示例**：常规 http web 服务 → `http_rest/`；分布式 web 服务接游戏
> 后端 → `http_rpc/`；分布式游戏服务（长连接网关 + 跨 module RPC）→ `single_process/`。

## 依赖

- 大部分示例**不需要 Redis / MySQL / Consul**，直接 `go run` 即可
- `db_cache/` 的 Redis 操作部分需要实际 Redis（无 Redis 时静默跳过，不 panic）
- `single_process/` 和 `rpc_policy/` 会启动 TCP 监听（默认 :8082），注意端口冲突
- `http_rest/` 监听 :8090、`http_rpc/` 监听 :8091（HTTP），注意端口冲突

## v2 特性速查

| v2 特性 | 示例文件 | 关键代码行 |
|---------|---------|-----------|
| `WithHTTPService(web.Options{...})` | http_rest/main.go | 构建 server 的 builder chain（HTTP 一等公民） |
| `web.Router` 路由 + `web.Auth` 鉴权 + `web.NewTokenBucketLimiter` | http_rest/main.go | Routes 回调内 |
| `web.BackendFromRequest` HTTP→RPC 桥 | http_rpc/main.go | getPlayerHandler / giveGiftHandler |
| `WithSingleProcess()` | single_process/main.go | 构建 server 的 builder chain |
| `WithGatewayOptions(...)` | single_process/main.go | GatewayOptions struct |
| `SendRPCMessage` 进程内直通 | single_process/main.go | UserService.Login → ShopGiveGift |
| `WithMethodPolicy` | rpc_policy/main.go | 三种策略场景 |
| `config.Load()` / `Reload()` | config_reload/main.go | struct tag 驱动 |
| `metrics.NewCounter/Gauge/Histogram` | metrics_trace/main.go | 内存 Provider |
| `trace.StartSpan` | metrics_trace/main.go | Span + TraceID |
| `log.InfoTagW` (zap.Field) | log_usage/main.go | 零分配热路径 |
| `component.ReloadGameConf` | game_config/main.go | 手动 + watcher 热更 |
| `db.AutoCacheBuilder` | db_cache/main.go | 三种缓存模式 |
| `db.FailureQueue` | db_cache/main.go | 补偿队列 |
| `util.Go` / `GoE` | util_tools/main.go | 协程池 + fallback |
