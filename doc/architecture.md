# tgf v2 架构

> 最后更新：2026-04-11

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
 │  - WithMethodPolicy (C6) / WithMetrics (B4) / WithTracer (B4) │
 │  - WithHealthCheck (A6)                                       │
 └───────────────────────────┬────────────────────────────────────┘
                             │
        ┌────────────────────┴────────────────────────┐
        │                                             │
 ┌──────▼─────────┐    ┌────────────▼────────────┐    │
 │  Gateway       │    │  RPC Server             │    │
 │  - TCP / WS    │    │  - rpcx service registry│    │
 │  - KCP (A8)    │    │  - Consul discovery     │    │
 │  - IConn (A2)  │    │  - Method policy (C6)   │    │
 │  - login lock  │    │  - Metrics / Trace      │    │
 │    (A3)        │    │                         │    │
 └──────┬─────────┘    └────────────┬────────────┘    │
        │                           │                  │
 ┌──────▼───────────────────────────▼──────────────────▼──────────┐
 │  Data layer                                                    │
 │  - autoCacheManager (write-behind, A1 + A1b FailureQueue)     │
 │  - game_config (C5 hot reload)                                │
 │  - config/ (C3 struct-tag driven)                             │
 └────────────────────────────────────────────────────────────────┘
```

## 关键包

| 包 | 职责 | v2 变更要点 |
|----|------|------------|
| `tgf` | 顶层入口、环境变量常量、错误类型 | ErrConnClosed / ErrConnSendTimeout / ErrUserNotFound 新增 |
| `tgf/rpc` | 网关、IService、rpcx 封装 | A2 IConn / A3 login lock / A4 builder hooks / A7+C6 policy / A8 KCP / C1 unified gateway / C2 sub-interfaces |
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

## 关键文件索引

- `tgf/rpc/rpcserver.go` — Server builder、生命周期、Optional 管道
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
