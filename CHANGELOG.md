# Changelog

本文件记录 tgf 主模块的版本变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号采用语义化版本（SemVer）。v2 大版本允许破坏性 API 变更，具体迁移指引见各 A/B/C 档阶段报告
与未来的 `doc/migration-v1-to-v2.md`。

## [Unreleased]

迁移指南：`doc/migration-v1-to-v2.md`
架构图：`doc/architecture.md`
可观测性接入：`doc/observability.md`

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
