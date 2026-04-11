# Changelog

本文件记录 tgf 主模块的版本变更。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号采用语义化版本（SemVer）。v2 大版本允许破坏性 API 变更，具体迁移指引见各 A/B/C 档阶段报告
与未来的 `doc/migration-v1-to-v2.md`。

## [Unreleased]

### B 档 - 工程化与可观测性（进行中）

- **B2 构建基线**：新增 `Makefile`、`.golangci.yml`、`Dockerfile`、GitHub Actions `ci.yml`、`CHANGELOG.md`。
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
