# tgf v1 → v2 迁移指南

> 最后更新：2026-04-11
> 目标读者：从 v1.x 升级到 v2.0 的业务开发者

本文档按档位列出所有破坏性变更和兼容性要点。**建议按顺序操作**——很多变更是
纯"新增 API"或"薄兼容包装"，只有少数需要改代码的地方。

## 按时间线看迁移难度

| 档 | 破坏性 | 迁移工作量 |
|----|--------|-----------|
| A 档稳定性 | **Send/ToUser 返回 error**；`ITCPBuilder.WriteTimeout` 新增 | 低 |
| B 档工程化 | 无破坏 | 零 |
| C1 Server builder | 无破坏（老 API 薄包装） | 零 |
| C2 IService | `GetLogicSyncMethod` → `LogicSyncMethods`（静默失效风险） | **必改** |
| C3 配置系统 | 无破坏（新老并存） | 零 |
| C4 DB 补偿队列 | 无破坏（opt-in） | 零 |
| C5 GameConfig 热更 | 无破坏 | 零 |
| C6 RPC 策略化 | 无破坏（新增 API） | 零 |

整体迁移工作量：**大部分项目改 1 处（C2 的方法名），可选地按需用新 API**。

---

## A 档：稳定性修复（v2-alpha-stageA）

### A2-phase3 · Send / ToUser 返回 error

**变更**：`tcpService.Send(data []byte)` 和 `tcpService.ToUser(userId, data)` 从
`返回 void` 改为 `返回 error`。

**影响**：所有调用点要处理 error 或显式忽略。

```go
// v1
user.Send(payload)

// v2
if err := user.Send(payload); err != nil {
    log.Warn("send failed: %v", err)
    // 或者
}
// 或显式忽略（不推荐）
_ = user.Send(payload)
```

新增的 sentinel error：
- `tgf.ErrConnClosed` — 连接已关闭
- `tgf.ErrConnSendTimeout` — 发送超时
- `tgf.ErrUserNotFound` — 目标用户不存在

### A2-phase3 · ITCPBuilder 新增 WriteTimeout

**变更**：`rpc.ITCPBuilder` 接口新增 `WriteTimeout() time.Duration` 和
`WithWriteTimeout(d) *tcpBuilder` 方法。

**影响**：实现了 `ITCPBuilder` 的自定义 builder 需要补这两个方法。
大多数业务方直接用框架内置的 `newTCPBuilder()`，**零改动**。

默认值从 v1 的硬编码 10 分钟魔数改为 5 秒，可配置：

```go
builder := rpc.NewTCPBuilder().WithWriteTimeout(10 * time.Second)
```

### A3 · 跨节点登录协调

**变更**：`gate.Login` 内部重写为 "Redis 锁 + 定向踢 owner" 模式，不再使用
`BorderRPCMessage` 广播踢人。

**影响**：**无需改业务代码**。登录流程对调用方透明。如果你的项目自己实现了
`BorderRPCMessage` 消费逻辑用来处理踢人事件，v2 后这些消息不再触发——可以
直接删掉对应的处理器。

### A4 · Server builder 分组 Hook

**变更**：`NewRPCServer()` 不再隐式装载 Consul discovery hook 和 RPC client。
默认行为仍然自动装上，但可以用 `WithoutConsul()` / `WithoutServiceClient()`
显式关闭。

**影响**：**无破坏**——默认行为完全保持。只是多了关闭开关。

### A5 · util 陷阱清理

**变更**：
- `util/timer.go` 删除（空壳 AddTicker）
- `util/common.go` 删除（破的 IsNil）

**影响**：如果业务代码 import 了 `util.AddTicker` 或 `util.IsNil`，编译会
失败。这两个 API 在 v1 里本来就没实现，不应有真实调用。

### A7 · RPC 超时策略化

**新增**：`rpc.SetDefaultRPCTimeout(d)` / `rpc.SetMethodRPCTimeout(name, d)` /
`Server.WithDefaultRPCTimeout(d)` / `Server.WithMethodTimeout(name, d)`。

**影响**：纯新增 API，**零破坏**。原先硬编码的 5 秒超时现在可以按方法覆盖。

### A8 · KCP 网关

**新增**：`rpc.NewKCPBuilder(port)` + `Server.WithGatewayKCP(builder)` + AEAD
加密。

**影响**：纯新增 API，**零破坏**。

---

## B 档：工程化与可观测性（v2-alpha-stageB）

### B1 · Go 版本

**变更**：`go.mod` 和 `go.work` 统一到 Go 1.24.7。

**影响**：需要 Go 1.24+ 工具链。

### B2 · 构建基线

**新增**：`Makefile` / `.golangci.yml` / `Dockerfile` / GitHub Actions CI。

**影响**：业务方继承 CI 配置作为模板可选。

### B4 · metrics / trace 接口包

**新增**：`tgf/metrics` 和 `tgf/trace` 两个新包。

```go
server.WithMetrics(metrics.NoopProvider())   // 默认
server.WithTracer(trace.NoopTracer())         // 默认

// 业务方接入 Prometheus 时写一个 adapter:
import "your-repo/tgf-prom"
server.WithMetrics(tgfprom.NewProvider())
```

**影响**：纯新增，**零破坏**。

### B5 · 日志热路径优化

**新增**：`log.InfoW(msg, fields...)` 等 `zap.Field` 风格 API。

**影响**：老的 `log.InfoTag(tag, format, args...)` 完全保留。新代码建议用
`*W` 系列避免 Sprintf 分配：

```go
// v1 风格（继续可用）
log.DebugTag("gate", "连接接入 addr=%s fd=%d", addr, fd)

// v2 推荐
log.DebugTagW("gate", "连接接入", zap.String("addr", addr), zap.Int("fd", fd))
```

老 API 会在 v2 后续小版本标 `// Deprecated:`。

---

## C 档：API 演进（v2-alpha-stageC）

### C1 · Server builder 重构

**新增**：`rpc.GatewayOptions` 结构 + `Server.WithGatewayOptions(opt)` + `Server.WithStandalone()`。

**v1 的 4 个老方法薄包装保留**：`WithGateway / WithGatewayWS / WithGatewayWSS /
WithGatewayKCP` 全部带 `// Deprecated:` 注释，内部转发到新 API。

```go
// v1（继续可用，但 deprecated）
rpc.NewRPCServer().
    WithGateway("8082").
    WithGatewayWS("8082", "/ws")

// v2 推荐
rpc.NewRPCServer().
    WithGatewayOptions(rpc.GatewayOptions{
        TCPPort: "8082",
        WSPath:  "/ws",
        KCP:     rpc.NewKCPBuilder("8300").WithAEADKey(key),
    })
```

单机模式：

```go
// v1 需要两个 option
rpc.NewRPCServer().
    // ... 不装 Consul 没法简单做到

// v2
rpc.NewRPCServer().
    WithStandalone().
    WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).
    WithService(myService).
    Run()
```

**影响**：无破坏，老 API 继续工作。

### C2 · IService 接口重塑 ⚠️ 必改

**变更**：
- `GetLogicSyncMethod()` 方法**重命名**为 `LogicSyncMethods()`
- 新增 `IStatefulService` / `IUserLifecycleService` 可选子接口（Module 基类自动满足）

**影响**：

⚠️ **这是 v2 唯一要求业务代码手动修改的地方**。

如果你的 service 在 v1 里 override 了 `GetLogicSyncMethod`：

```go
// v1
func (m *MyService) GetLogicSyncMethod() []string {
    return []string{"OrderCreate", "OrderPay"}
}
```

改为：

```go
// v2
func (m *MyService) LogicSyncMethods() []string {
    return []string{"OrderCreate", "OrderPay"}
}
```

**⚠️ Go 不会因为方法名错误而编译失败**——漏改一个 override 会**静默失效**，
之前被标记为串行的方法变成默认并发，可能触发业务侧的并发 bug。

**迁移步骤**：

```bash
# 1. 先全量搜索
git grep -n "GetLogicSyncMethod"

# 2. 对每个找到的点，确认是 Module 子类的 override，改名为 LogicSyncMethods
# 3. 改完再搜一次，确认没漏
git grep -n "GetLogicSyncMethod"
# 应该只剩 deprecated 注释里的引用
```

没 override 的服务**不受影响**——`Module` 基类的默认实现已经一起改名。

### C2 · 可选子接口的使用

嵌入 `rpc.Module` 的业务 service **自动满足**两个新子接口，无需任何改动。
框架代码可以做结构化类型断言：

```go
for _, svc := range services {
    if stateful, ok := svc.(rpc.IStatefulService); ok {
        // 可以接收 state 通知
    }
    if lc, ok := svc.(rpc.IUserLifecycleService); ok {
        lc.AddUserLoginHook(globalLoginHook)
    }
}
```

### C3 · 新配置系统

**新增**：`tgf/config` 包。

**老系统保留**：`tgf.GetStrConfig[T]` / `tgf.Environment` 常量完全保留，两套并存。

迁移方向（渐进）：

```go
// v1
logLevel := tgf.GetStrConfig[string](tgf.EnvironmentLoggerLevel)

// v2
cfg := config.Current()
logLevel := cfg.Logger.Level
```

**影响**：无破坏，按需迁移。

### C4 · DB 补偿队列

**新增**：`db.FailureQueue` 接口 + `NoopFailureQueue` / `MemoryFailureQueue` /
`FileFailureQueue` 三种实现 + `AutoCacheBuilder.WithLongevityFailureQueue(q)` +
`db.ReplayFailureQueue(q, flushFn)`。

**影响**：纯新增 opt-in，**零破坏**。默认 NoopFailureQueue 等同于 A1 行为。

### C5 · GameConfig 热更

**新增**：`component.ReloadGameConf()` / `component.OnReload(fn)` /
`component.StartConfigWatcher()` / `component.StopConfigWatcher()`。

**影响**：纯新增，**零破坏**。`InitGameConfToMem` / `GetGameConf` 等老 API
完全保留。

### C6 · RPC 策略化完整版

**新增**：`rpc.MethodPolicy` 结构 + `rpc.SetMethodPolicy` / `Server.WithMethodPolicy` +
`ErrRPCOverload` / `ErrRPCRateLimited` / `ErrRPCCircuitOpen` 三个新错误值。

**影响**：纯新增 opt-in。如果业务不调 `SetMethodPolicy`，行为和 A7 一致（只有
timeout 策略）。

---

## 不在 v2 范围（路线图延后）

以下功能在路线图中列过但 v2 未落地，留给后续小版本：

- **rpcx / rpcx-consul fork 的主动依赖升级**：`hashicorp/consul/api` v1.8.1 →
  最新版需要改 fork，本轮约束不动
- **DB 层真正的分库分表**：需要重构 `sqlBuilder` 的构造时表名绑定，C4 已
  显式延后
- **GameConfig 更细粒度的单文件热更**：当前是全量 reload
- **RPC Adaptive rate limiting / 分布式断路器**：C6 只做了单节点
- **sonic v1.15 升级**：Windows + Go 1.24 下历史有回归，保留 v1.12.7

---

## 升级 checklist

把这份清单打勾过一遍就基本 OK：

- [ ] Go 工具链升级到 1.24+
- [ ] `go mod tidy` 拉新的 fsnotify v1.9.0 + 其它 B3 依赖
- [ ] 全量搜索 `GetLogicSyncMethod` 改为 `LogicSyncMethods`
- [ ] `Send` / `ToUser` 调用点处理返回的 error
- [ ] 自定义 `ITCPBuilder` 实现补 `WriteTimeout` / `WithWriteTimeout`
- [ ] 删掉对 `util.AddTicker` / `util.IsNil` 的引用（如果有）
- [ ] （可选）用 `WithGatewayOptions` 替代老 4 个网关方法
- [ ] （可选）用 `WithStandalone()` 替代手动两个 `Without*`
- [ ] （可选）用 `config.Current()` 替代 `tgf.GetStrConfig`
- [ ] （可选）用 `log.InfoTagW` 替代 `log.InfoTag` 热路径
- [ ] 跑 `go test -race ./...` 确认回归通过
- [ ] 跑 `go vet ./...` 零告警
