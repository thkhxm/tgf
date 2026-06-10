# Changelog

本文件记录 tgf 主模块的版本变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号采用语义化版本（SemVer）。v2 大版本允许破坏性 API 变更，具体迁移指引见
`doc/migration-v1-to-v2.md` 与各 A/B/C 档阶段报告。

## [Unreleased]

迁移指南：`doc/migration-v1-to-v2.md`
架构图：`doc/architecture.md`
可观测性接入：`doc/observability.md`
v3 整改路线图：`reports/V3-evaluation-and-roadmap.md`

### Changed

- **Go 版本升级到 1.26**：`go.work` 与三个 `go.mod`（tgf / rpcx / rpcx-consul）统一声明
  `go 1.26.0` + `toolchain go1.26.4`（2026-06 最新稳定线，Go 1.24 已脱离支持窗口）。
  实测 sonic v1.15.0 在 1.26 下编译/测试全绿（v2 早期因 sonic 与 1.26 不兼容而锁定 1.24.7，
  该问题已随 sonic 升级消除）。CI `setup-go` 同步到 `1.26.x`。fork tag 随之 bump 到
  `rpcx/v2 v2.0.3`、`rpcx-consul/v2 v2.0.2`。

### v3-D 档 · 止血与发布可用（进行中）

> 目标：消灭全部 P0，让框架"对外存在"。详见路线图 `reports/V3-evaluation-and-roadmap.md` 第 3.1 节。

- **D1 文档与发布**（Owner C）：
  - README 安装段去掉指向不存在远端 tag 的 `go get ...@v2-alpha.2`，改为
    `go get ...@latest` + 下游必须复制的 replace 块模板（fork 迁移完成后可省略）。
  - 删除文档中虚构的 SIGHUP / HTTP admin 热更触发表述（框架未内置信号处理）。
  - 修正迁移指南中不存在的 `rpc.NewTCPBuilder()` 示例，改为环境变量 `TCPWriteTimeoutMs`。
  - 技术选型表 sonic 版本对齐到 v1.15.0；rpcx/rpcx-consul 标注 fork tag。
  - README "全绿"声明补 workspace 前置条件说明。
- **D6 凭据卫生**（Owner C）：
  - `.gitignore` 新增 `.env` / `.env.*`（保留 `*.example` 模板例外）、`log/`、`common/`、`*.md5`。
  - 新增占位值模板 `.env.example` / `.env.test.example` / `.env.release.example`（可提交）。
  - README 新增"配置与凭据"段：真实凭据放 `.env.<module>`（已 gitignore），从模板复制。
- **CI**（Owner C）：workflow 使用 `setup-go 1.24.x`，区分 unit（无依赖）与 integration（带 tag）两条 job。
- 其余 D 档项（D2 串包 / D3 优雅停机 / D4 单进程网关 / D5 吞错止血 / D7 登录鉴权）见
  各自 Owner 的代码改动与本档收尾汇总。

### C9 · 配置参数暴露 + 硬编码审计

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

### C8 · 单进程模式 / In-Process RPC 直通

- 新增 `rpc.WithInProcessDispatch()` / `rpc.WithSingleProcess()` builder 方法
- 新增 `localDispatcher` 反射调度器：`SendRPCMessage` 先查本地注册 service，
  命中则反射调用绕开 rpcx/Consul，策略管道和 metrics 埋点依然生效
- 配套修复 A4 遗留 bug：`WithoutConsul()` 下 `service.Startup()` 被跳过，
  现在 Startup 与 discovery 注册完全解耦
- 18 个新测试

### C 档 - API 与架构演进

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

### B 档 - 工程化与可观测性

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
