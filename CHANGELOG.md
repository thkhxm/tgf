# Changelog

本文件记录 tgf 主模块的版本变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号采用语义化版本（SemVer）。v2 大版本允许破坏性 API 变更，具体迁移指引见
`doc/migration-v1-to-v2.md` 与各 A/B/C/D/E/F/G 档阶段报告。发版流程见 `doc/release-process.md`。

## [Unreleased]

迁移指南：`doc/migration-v1-to-v2.md`
架构图：`doc/architecture.md`
可观测性接入：`doc/observability.md`
发版流程：`doc/release-process.md`

## [2.0.0] - 2026-06-11

tgf 首个正式发布版本。在 v2-alpha（A/B/C 档：稳定性修复 + 工程化 + API 演进）的基础上，
v3 阶段以 **D（止血与发布可用）/ E（接线收尾）/ F（生产化地基）/ G（HTTP 一等公民）**
四档完成"对外可用"的最后一公里，并完成 **Go 1.26 工具链升级**与 **module path `/v2`
迁移**。本版本允许相对 v1 的破坏性 API 变更，迁移要点见 `doc/migration-v1-to-v2.md`
（实际只有 C2 的 `GetLogicSyncMethod → LogicSyncMethods` 一处必改）。

> **module path 迁移**：自本版本起 tgf 主模块 path 为 `github.com/thkhxm/tgf/v2`
> （Go SIV 规则：major ≥ 2 必须在 path 末尾带 `/v2`）。`go get github.com/thkhxm/tgf/v2@v2.0.0`，
> import 形如 `github.com/thkhxm/tgf/v2/rpc`。依赖的 rpcx / rpcx-consul fork 也迁移为
> `github.com/thkhxm/rpcx/v2`（tag `v2.0.3`）/ `github.com/thkhxm/rpcx-consul/v2`
> （tag `v2.0.2`），由 tgf `go.mod` 直接 `require` 并随之自动拉取——**下游业务工程无需
> 任何 `replace`**。

### Added

- **G 档 · HTTP 一等公民**：HTTP web 服务与 RPC 平级。
  - `Server.WithHTTPService(web.Options{...})`：同进程起标准 HTTP 服务，共享生命周期与
    优雅停机，可多次调用挂多个端口。
  - `Server.WithHTTPServiceConsul(opt, reg HTTPRegistration)`：HTTP 服务注册进 Consul
    （自动挂 health 路由、TTL 续约或主动 HTTP 探测、`Destroy` 时先摘流量再 drain），使
    分布式 web 服务可被标准 Consul 生态（DNS / API / fabio / traefik）发现与负载均衡。
  - `Server.WithClientOnly()`：纯 web/client 接入进程——不注册 service、不创建 rpcx
    server、不监听 rpcx 端口、不进入 RPC 服务发现，但保留 Consul discovery + RPC client，
    `web.Backend` / `SendRPCMessage` 可跨节点调后端（`Run` 时 `validateClientOnly` fail-fast）。
  - `tgf/web` 自包含包：基于 Go 1.22 `net/http.ServeMux` 的 `Router`（`"GET /users/{id}"`
    模式 + 路径参数）+ `Group`/`Use`、内置中间件链（Trace / AccessLog / Metrics / Recover /
    RateLimit / Auth，外→内可单独 Disable）、令牌桶限流 `web.NewTokenBucketLimiter(qps)`、
    鉴权 `web.Auth(web.StaticBearerToken/BearerToken)`（constant-time + fail-closed）、
    HTTP→RPC 桥 `web.BackendFromRequest(r).Invoke(ctx, module, method, &args, &reply)`
    （单进程 localDispatcher 直通 / 分布式 `SendRPCMessageByStr` 自动选路，traceId 经
    `X-Trace-Id` 全链路透传）。`web` 不 import `rpc`（无 import 环）。
  - 新增配置项 `HTTPPort`(8090) / `HTTPReadHeaderTimeoutSec`(5) / `HTTPShutdownTimeoutSec`(10)，
    登记于 `tgf/config.HTTPConfig` 与 `define.go` `Environment` 常量。
- **C8 单进程模式 / In-Process RPC 直通**：`rpc.WithSingleProcess()` / `WithInProcessDispatch()`
  + `localDispatcher` 反射调度器——`SendRPCMessage` 先查本地注册 service，命中则反射调用
  绕开 rpcx/Consul，策略管道与 metrics 埋点依然生效。**零 Consul 依赖**，适合单元测试与小型部署。
- **C6 RPC 策略化完整版**：`MethodPolicy{Timeout, MaxConcurrency, RateLimit, CircuitBreaker}`
  + `SetMethodPolicy` / `Server.WithMethodPolicy`，集成 metrics 拒绝路径计数。
- **C5 GameConfig 热更**：`component.ReloadGameConf()` / `OnReload(fn)` / `StartConfigWatcher()`
  / `StopConfigWatcher()`，基于 fsnotify v1.9.0 的目录监听 + 200ms debounce + 双 buffer 原子切换。
- **C4 DB 补偿队列（A1b）**：`FailureQueue` 接口 + `NoopFailureQueue` / `MemoryFailureQueue`
  / `FileFailureQueue` 三种实现 + `AutoCacheBuilder.WithLongevityFailureQueue(q)`
  + `db.ReplayFailureQueue(q, flushFn)`，落库失败跨重启恢复。
- **C3 配置系统升级**：新建 `tgf/config` 包，struct tag 驱动的反射加载器（`Config / Load /
  Reload / Current / OnReload`，支持 string/int/uint/float/bool/time.Duration/[]string）。
  零新依赖，老 `tgf.GetStrConfig` 保留。
- **C1 Server builder 重构**：`GatewayOptions` 统一入口 + `WithGatewayOptions` / `WithStandalone`
  （老 `WithGateway / WithGatewayWS / WithGatewayWSS` 保留为 deprecated 薄包装，零破坏）。
- **B5 日志 zap.Field 风格 API**：`InfoW / DebugW / WarnW / ErrorW / InfoTagW / ...`（零分配热路径），
  老 `log.*Tag` Sprintf 风格保留。
- **B4 metrics / trace 接口包**：新建 `tgf/metrics`、`tgf/trace`——`Counter / Gauge / Histogram /
  Provider` + `Span / Tracer` 接口，默认 NoOp 零外部依赖；`Server.WithMetrics(p)` /
  `Server.WithTracer(t)`。框架内建 7 个埋点（`tgf_gate_connections` / `tgf_rpc_latency_ms`
  / `tgf_rpc_calls_total` / `tgf_rpc_policy_reject_*` 等）自动触发。
- **B2 构建基线**：`Makefile`、`.golangci.yml`、`Dockerfile`、GitHub Actions `ci.yml`、`CHANGELOG.md`。
- **C9 配置参数暴露**：暴露 17 个新环境变量（log 包 8 个 lumberjack 参数、MySQL 连接池 3 个、
  RPC/网关运行参数 4 个、DB 缓存超时 2 个）到 v1 老 API + v2 新 config struct 双系统；
  零配置时行为与 v1 完全一致。详见 `reports/C9-config-exposure-and-audit.md`。

### Changed

- **module path 迁移到 `/v2`**：tgf 主模块 `github.com/thkhxm/tgf` → `github.com/thkhxm/tgf/v2`；
  rpcx / rpcx-consul fork 同步迁移为 `github.com/thkhxm/rpcx/v2`（tag `v2.0.3`）/
  `github.com/thkhxm/rpcx-consul/v2`（tag `v2.0.2`），由 tgf `go.mod` 直接 require 自动带入，
  **下游无需任何 replace**。README / doc 全部示例 import 路径随之更新。
- **Go 版本升级到 1.26**：`go.work` 与三个 `go.mod`（tgf / rpcx / rpcx-consul）统一声明
  `go 1.26.0` + `toolchain go1.26.4`（2026-06 最新稳定线，Go 1.24 已脱离支持窗口）。
  实测 sonic v1.15.0 在 1.26 下编译/测试全绿（v2 早期因 sonic 与 1.26 不兼容而锁定 1.24.7，
  该问题已随 sonic 升级消除）。CI `setup-go` 同步到 `1.26.x`。
- **C2 IService 接口重塑（破坏性，必改）**：`GetLogicSyncMethod` 重命名为 `LogicSyncMethods`
  （**Go 不会因方法名错误编译失败，漏改的 override 会静默失效**——被标记串行的方法退化为
  并发，可能触发业务并发 bug，迁移时务必全量搜索改名）。新增 `IStatefulService` /
  `IUserLifecycleService` 可选子接口，`Module` 基类自动满足。
- **E 档 · 接线收尾**：策略管道全覆盖（HTTP→RPC 桥与 RPC 调用共用 E1 限流/熔断 + A7 超时
  + E3 埋点）、配置系统收敛（E2：`tgf.InitConfig` 经 `config.LoadLenient` 单次解析，`tgf/config`
  成为唯一真相源，`tgf.GetStrConfig` 退化为同一规范化快照的薄适配，bool 统一规范化为 `"1"/"0"`；
  `config.Reload()` 先重读 `.env.<module>` 再原子替换快照、解析失败保留旧配置）、数据层故障路径接线。
  历史 `-TGFMODULE` flag 为 dead code，E2 已移除，环境变量 `TGFMODULE` 是唯一入口。
- **F 档 · 生产化地基**：fork 治理（rpcx / rpcx-consul 迁移为带 `/v2` 后缀的自有 module path
  并发 tag）、Consul TTL check + serve-ready-before-register 时序（F2）、会话与踢人收尾、过载保护。
- **依赖升级（B3）**：`go-sql-driver/mysql v1.9.3`、`panjf2000/ants/v2 v2.12.0`、
  `xuri/excelize/v2 v2.10.1`、`go.uber.org/zap v1.27.1`、`protobuf v1.36.11` + `golang.org/x/*` 跟随。
- **A 档破坏性变更**：`Send` / `ToUser` 返回 `error`；`ITCPBuilder` 新增 `WriteTimeout` /
  `WithWriteTimeout`（默认值由 v1 硬编码 10 分钟改为 5 秒，可经 `TCPWriteTimeoutMs` 环境变量调整）；
  新增 sentinel error `tgf.ErrConnClosed` / `ErrConnSendTimeout` / `ErrUserNotFound`。
- **B5 日志热路径优化**：`log.*Tag` 系列前置 level + tag 过滤，避免被 level 过滤时的无谓 Sprintf。

### Fixed

- **B6 测试补齐 + race 修复**：新增 `component/game_config_concurrent_test.go`（5 用例），
  修 `db/cacheData.state` 无原子保护 + `GameConfig.getCacheGameConfData` double-check 破损。
- **B2 顺带修复 5 处 pre-existing bug**：`fmt.Sprintf` 丢字符串 / `newConsulServerInfo` 越界 /
  `pool_test.TestGo` 空壳 / `atomic.Pointer` 复制。6 个集成测试改为 `//go:build integration` 隔离。
- **C8 配套修复 A4 遗留**：`WithoutConsul()` 下 `service.Startup()` 被跳过——Startup 与
  discovery 注册完全解耦。
- **D4 单进程网关**：单进程 + 网关组合下客户端首条 Logic 帧在 `doLogic → sendMessage →
  getRPCClient` 对 nil discovery panic（P0-2）已修——`sendMessage` 在单进程模式走 localDispatcher
  本地直通，真实 TCP/WS/KCP 客户端消息可端到端到达本地注册的 service。
- **A 档稳定性修复**：TCP 网关 `if !close` 反语义导致 `doLogic` 从未触发；Offline/Stop 双发
  stop 通道 panic；WebSocket idle deadline 缺失导致僵尸连接无法回收；`util/pool.Go` 池满静默
  丢任务（→ fallback 裸 goroutine + stderr）；`util/weight.Hit` 非原子（→ CAS 循环）；
  `internal/consul.RegisterDiscovery` 重复注册连接泄漏。

### Security

- **D7 登录鉴权（默认 fail-closed）**：`gate.Login` 默认要求 `LoginReq.Token`，由默认 HMAC token
  实现（密钥 env `LoginTokenSecret` 或 `WithLoginTokenSecret(...)`）或 `WithLoginCheck` 注入的
  自定义校验器验证——**伪造 userId / 过期 token / 无 token 一律拒绝**。业务侧用
  `rpc.GenerateLoginToken(userId, ttl)` 签发、`rpc.UserLoginWithToken(ctx, userId, token)` 登录；
  本地开发可显式 `WithoutLoginCheck()` 恢复旧行为。
- **A3 跨节点登录原子化**：基于 Redis 锁 + 定向踢远端 owner，替代 v1 的 `BorderRPCMessage` 广播方案，
  消除并发登录竞态。
- **A8 KCP + AEAD 网关**：第三种传输通道，ChaCha20-Poly1305 帧加密，复用 `IConn` 生命周期。
- **D6 凭据卫生**：`.gitignore` 新增 `.env` / `.env.*`（保留 `*.example` 模板例外）、`log/` 等；
  新增占位值模板 `.env.example` / `.env.test.example` / `.env.release.example`；真实凭据放
  `.env.<module>`（已 gitignore），从模板复制，绝不入库。
- **HTTP web 鉴权**：`web.Auth` 比对走 `subtle.ConstantTimeCompare` 防时序侧信道；未配置口令 →
  503（fail-closed），带错/缺口令 → 401。HTTP `http.Server` 实例带 `ReadHeaderTimeout` 防 slowloris。

### 各档逐项明细（A/B/C/D 档原始变更，按档位归档）

> 上方 Added/Changed/Fixed/Security 分组是 2.0.0 的对外摘要；以下按 A/B/C/D 档保留逐项
> 原始记录，便于追溯每条变更的来龙去脉。注意 B1 当时把工具链对齐到 Go 1.24.7，后由 v3
> 升级到 1.26（见上方 Changed 的「Go 版本升级到 1.26」），以最终 1.26 为准。

#### D 档 · 止血与发布可用（明细）

- **D1 文档与发布**：README 安装段改为 `go get github.com/thkhxm/tgf/v2@v2.0.0`（不再依赖
  不存在的远端 tag，下游无需 replace）；删除文档中虚构的 SIGHUP / HTTP admin 热更触发表述
  （框架未内置信号处理）；修正迁移指南中不存在的 `rpc.NewTCPBuilder()` 示例，改为环境变量
  `TCPWriteTimeoutMs`；技术选型表 sonic 对齐 v1.15.0、rpcx/rpcx-consul 标注 fork tag。
- **D6 凭据卫生**：`.gitignore` 新增 `.env` / `.env.*`（保留 `*.example` 模板例外）、`log/`、
  `common/`、`*.md5`；新增占位值模板 `.env.example` / `.env.test.example` / `.env.release.example`；
  README 新增「配置与凭据」段。
- **CI**：workflow 区分 unit（无依赖）与 integration（`//go:build integration`）两条 job，
  `setup-go` 随 Go 1.26 升级到 `1.26.x`。
- 其余 D 档项（D2 串包 / D3 优雅停机 / D4 单进程网关 / D5 吞错止血 / D7 登录鉴权）见各自
  Owner 的代码改动与本档收尾汇总。

#### C9 · 配置参数暴露 + 硬编码审计（明细）

针对用户反馈 log 包参数无法配置的问题，全面审计框架硬编码点并暴露
高优先级的 11 个新环境变量到 v1 老 API + v2 新 config struct 双系统。

**log 包**（8 个新 env，覆盖之前包私有的 lumberjack 参数）：
- `LogMaxSize` / `LogMaxAge` / `LogMaxBackups` / `LogCompress` / `LogLocalTime`
- `LogTimeFormat` / `LogServiceFile` / `LogDBFile`

**MySQL 连接池**（3 个新 env）：
- `MySqlMaxIdleConns` / `MySqlMaxOpenConns` / `MySqlConnMaxLifetimeSec`

**RPC / 网关运行参数**（4 个新 env）：
- `RPCDefaultTimeoutMs` / `TCPDeadLineSec` / `TCPWriteTimeoutMs` / `TCPSendChanTimeoutMs`

**DB 默认缓存超时**（2 个新 env）：
- `DBCacheTimeoutSec` / `DBMemTimeoutSec`

`tgf/config/config.go` 对应新增 `RPCConfig` / `DBConfig` 子 struct 并扩展
`LoggerConfig` / `MySQLConfig`。零配置时所有行为和 v1 完全一致。
新增 7 个测试。详细审计报告见 `reports/C9-config-exposure-and-audit.md`。

#### C8 · 单进程模式 / In-Process RPC 直通（明细）

- 新增 `rpc.WithInProcessDispatch()` / `rpc.WithSingleProcess()` builder 方法
- 新增 `localDispatcher` 反射调度器：`SendRPCMessage` 先查本地注册 service，
  命中则反射调用绕开 rpcx/Consul，策略管道和 metrics 埋点依然生效
- 配套修复 A4 遗留 bug：`WithoutConsul()` 下 `service.Startup()` 被跳过，
  现在 Startup 与 discovery 注册完全解耦
- 18 个新测试

#### C 档 - API 与架构演进（明细）

- **C7 文档**：`doc/architecture.md` + `doc/migration-v1-to-v2.md` + `doc/observability.md`。
- **C6 RPC 策略化完整版**：基于 A7 超时骨架扩展 `MethodPolicy{Timeout, MaxConcurrency, RateLimit, CircuitBreaker}`。
  新增 `SetMethodPolicy` / `Server.WithMethodPolicy`，新错误值 `ErrRPCOverload` / `ErrRPCRateLimited` / `ErrRPCCircuitOpen`。
  集成 B4 metrics 的拒绝路径计数。
- **C4 DB 补偿队列（A1b）**：新增 `FailureQueue` 接口 + `NoopFailureQueue` / `MemoryFailureQueue` / `FileFailureQueue`。
  `AutoCacheBuilder.WithLongevityFailureQueue(q)` + `db.ReplayFailureQueue(q, flushFn)`。分库分表显式延后。
- **C2 IService 接口重塑**：`GetLogicSyncMethod` 重命名为 `LogicSyncMethods`（**破坏性**，静默失效风险）。
  新增 `IStatefulService` / `IUserLifecycleService` 可选子接口，Module 自动满足。
- **C1 Server builder 重构**：新增 `GatewayOptions` 统一入口 + `WithGatewayOptions` / `WithStandalone`。
  老 `WithGateway / WithGatewayWS / WithGatewayWSS` 保留为 deprecated 薄包装，零破坏。
- **C3 配置系统升级**：新建 `tgf/config` 包，struct tag 驱动的反射加载器。`Config / Load / Reload / Current / OnReload`。
  支持 string / int / uint / float / bool / time.Duration / []string 类型。零新依赖。老 `tgf.GetStrConfig` 保留。
- **C5 GameConfig 热更**：`component.ReloadGameConf` / `OnReload(fn)` / `StartConfigWatcher` / `StopConfigWatcher`。
  基于 fsnotify v1.9.0 的目录监听 + 200ms debounce。双 buffer 原子切换。

#### B 档 - 工程化与可观测性（明细）

- **B6 测试补齐 + race 修复**：新增 `component/game_config_concurrent_test.go`（5 用例）。
  顺手修 `db/cacheData.state` 无原子保护 + `GameConfig.getCacheGameConfData` double-check 破损。
- **B5 日志热路径优化**：`log.*Tag` 系列前置 `logger.Check(level, "")` + tag 过滤，避免被 level 过滤时的无谓 Sprintf。
  新增 `InfoW / DebugW / WarnW / ErrorW / InfoTagW / ...` zap.Field 风格 API（零分配）。
- **B4 metrics / trace 接口包**：新建 `tgf/metrics` 和 `tgf/trace` 两个包。
  `Counter / Gauge / Histogram / Provider` + `Span / Tracer` 接口，默认 NoOp 零外部依赖。
  `Server.WithMetrics(p)` / `Server.WithTracer(t)`。两处埋点示范：`tgf_gate_connections` / `tgf_rpc_latency_ms`。
- **B3 依赖升级**：`go-sql-driver/mysql v1.9.3`、`panjf2000/ants/v2 v2.12.0`、`excelize/v2 v2.10.1`、
  `go.uber.org/zap v1.27.1`、`protobuf v1.36.11` + `golang.org/x/*` 跟随。sonic / consul 跳过。
- **B2 构建基线**：新增 `Makefile`、`.golangci.yml`、`Dockerfile`、GitHub Actions `ci.yml`、`CHANGELOG.md`。
  顺带修 5 处 pre-existing bug（`fmt.Sprintf` 丢字符串 / `newConsulServerInfo` 越界 / `pool_test.TestGo` 空壳 /
  `atomic.Pointer` 复制）。6 个集成测试改为 `//go:build integration` 隔离。
- **B1 Go 版本对齐**：`go.mod` / `go.work` 统一到 Go 1.24.7（A1 阶段顺带完成）。

## [v2-alpha-stageA] - 2026-04-11

A 档稳定性修复完整落地，详见 `reports/A-stage-summary.md`。关键变更摘要：

### 新增

- **A8 KCP + AEAD 网关**：`WithGatewayKCP` / `NewKCPBuilder`，ChaCha20-Poly1305 帧加密，复用 A2 的 `IConn` 生命周期。
- **A7 RPC 超时策略化**：`WithDefaultRPCTimeout` / `WithMethodTimeout`，取代硬编码 5 秒。
- **A6 Consul 健康检查骨架**：`WithHealthCheck` / `IsHealthy`，注册流程幂等化。
- **A4 Server builder 分组**：`WithoutConsul` / `WithoutServiceClient`，pre/post serve hooks 拆分。
- **A3 跨节点登录协调**：`loginCoordinator` 接口 + Redis 锁 + 定向踢远端 owner。
- **A2 IConn 抽象**：`conn.go` 统一 TCP/WS/KCP 生命周期，`handleConn` 单入口。
- **A1 write-behind 可靠性**：`WithLongevityGroupSize` / `WithLongevityRetry`，失败重试 + 补偿。

### 破坏性变更（v2）

- `Send` / `ToUser` 返回 error。
- `ITCPBuilder` 新增 `WriteTimeout` / `WithWriteTimeout`。
- 新增错误 `tgf.ErrConnClosed` / `ErrConnSendTimeout` / `ErrUserNotFound`。

### 修复

- TCP 网关 `if !close` 反语义导致 `doLogic` 从未触发（pre-existing）。
- Offline / Stop 双发 stop 通道 panic。
- WebSocket idle deadline 缺失导致僵尸连接无法回收。
- `util/pool.Go` 池满静默丢任务 → fallback 裸 goroutine + stderr。
- `util/weight.Hit` 非原子 → CAS 循环。
- `internal/consul.RegisterDiscovery` 重复注册连接泄漏。

### 删除

- `util/timer.go`（零调用点空壳）。
- `util/common.go`（破的 `IsNil[Val]`）。
- `statsview/` 目录（workspace 根级清理）。
