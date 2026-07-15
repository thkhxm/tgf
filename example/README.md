# tgf v2 示例与证据矩阵

每个已声明场景都必须同时指向：可运行的 example、自动化证据和外部
集成边界。`go test -count=1 ./example/...` 会编译全部示例包，并运行
不依赖 Consul/Redis/MySQL 的 handler/service 单测和本矩阵的治理测试。

## 证据等级

- **unit**：默认 `go test` 真实执行，不连外部服务。
- **compile**：同一条 `go test` 编译可执行包和 API 装配，不等于端口或
  外部中间件已运行。
- **integration**：必须显式启动示例及其依赖后验证；默认 CI 不伪造、
  不静默跳过，也不声称已通过。

## 场景矩阵

| 场景 ID | 可运行 example | 默认自动化证据 | 外部集成状态 |
|---|---|---|---|
| `single-process-game` | `single_process/` | unit：Shop handler；compile：单进程 server | 无 Consul/Redis/MySQL；网关运行需端口 |
| `distributed-game` | `distributed_game/cmd/{gateway,game}` | unit：UserService + KCP key；compile：双进程 | Consul/Redis 未在默认测试中运行 |
| `http-rest` | `http_rest/` | unit：GET/POST handler；compile：HTTP server | 手工 `go run` + curl，默认测试不绑端口 |
| `http-rpc` | `http_rpc/` | unit：PlayerService；compile：HTTP→RPC bridge | 单进程可手工运行；跨进程需 Consul |
| `gateway-tcp` | `distributed_game/cmd/gateway` | compile：`GatewayOptions.TCPPort` | 真实连接需 Consul/Redis + 启动网关 |
| `gateway-ws` | `distributed_game/cmd/gateway` | compile：`GatewayOptions.WSPath` | 真实连接需 Consul/Redis + 启动网关 |
| `gateway-kcp` | `distributed_game/cmd/gateway` | unit：32 字节 AEAD key；compile：KCP builder | 真实 UDP 需 Consul/Redis + 启动网关 |
| `robot-self-test` | `robot_test/` | unit：网关可达 GameService；compile：TCP/WS/KCP robot | `go run` 在本机起端口做自测，不在默认 CI 运行 |
| `redis-mysql-write-behind` | `db_cache/` | unit：MemoryFailureQueue；compile：write-behind builder | Redis/MySQL 仅在 `TGF_EXAMPLE_EXTERNAL=1` 时尝试 |
| `config-reload` | `config_reload/` | compile：`Load/Current/Reload/OnReload` | 示例自用进程 env，无外部中间件 |
| `game-config` | `game_config/` | compile：JSON load/reload/watcher | 手工运行使用临时目录 |
| `logging` | `log_usage/` | compile：Sprintf/zap.Field/tag 日志 API | 手工运行会写本地 log 产物 |
| `metrics-trace` | `metrics_trace/` | compile：MemoryProvider + span/traceId | 无外部 provider；Prometheus/OTel adapter 不在此验证 |
| `rpc-policy` | `rpc_policy/` | compile：限流/熔断/并发策略 | 手工运行会起本地 RPC 端口 |
| `util` | `util_tools/` | compile：Go/GoE/Snowflake/转换 | 无外部依赖 |

## 分布式与单进程的边界

- `single_process/` 只表示零 Consul 的单进程多 Module，不再代表分布式。
- `distributed_game/` 才是真实的长连接网关 + 独立业务服，具有
  `cmd/gateway` / `cmd/game` / `internal/api` / `internal/service` 布局。
- `http_rpc/` 默认可运行形态是单进程 HTTP→RPC；分布式 web 拆分步骤
  是 integration 指南，不是默认测试已执行的事实。

## 运行与停机

```bash
go test -count=1 ./example/...
go vet ./example/...

cd example/<directory>
go run .
```

长驻示例不自行 `signal.Notify`：`SIGINT/SIGTERM` 由 tgf 统一处理，
业务 `main` 只等待 `Run()` 返回的 done。外部依赖的运行方式见
`distributed_game/README.md` 和 `db_cache/main.go` 的显式开关。
