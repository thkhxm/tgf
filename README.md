[![Go Report Card](https://goreportcard.com/badge/github.com/thkhxm/tgf)](https://goreportcard.com/report/github.com/thkhxm/tgf)
[![Go Version](https://img.shields.io/badge/go-1.24%2B-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

# tgf

**tgf** 是一套基于 Go 语言的**分布式游戏服务器框架**，专注于解决游戏业务开发中常见的
稳定性、并发与运维问题。v2 在 v1 基础上做了系统性的重构——从网关连接生命周期到
数据落库可靠性，从可观测性接口到 API 一致性，都有明显的提升。

> 设计目标：让中小型团队与独立开发者**只关注业务逻辑**，不必处理连接风暴、
> 跨节点协调、配置热更、指标埋点这些底层细节。

## 目录

- [v2 特性亮点](#v2-特性亮点)
- [5 分钟快速上手](#5-分钟快速上手)
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
- **依赖升级** — Go 1.24.7、go-sql-driver/mysql、ants、excelize、protobuf 等保守升级到主线

### 🏗️ API / 架构演进（C 档）

- **GameConfig 热更** — `component.ReloadGameConf()` + `OnReload(fn)` + fsnotify 自动监听
- **配置系统升级** — 新 `tgf/config` 包，struct tag 驱动的反射加载器，新增字段只改一处
- **Server Builder 重构** — `WithGatewayOptions(...)` 合并 v1 四个平行 API，`WithStandalone()` 语义糖
- **IService 接口重塑** — 可选子接口 `IStatefulService` / `IUserLifecycleService`，Module 基类自动满足
- **DB 补偿队列** — `FailureQueue` 接口 + NoopImpl / MemoryImpl / FileImpl 三种实现
- **RPC 策略化完整版** — `MethodPolicy{Timeout, MaxConcurrency, RateLimit, CircuitBreaker}`，集成 metrics 拒绝路径计数
- **单进程模式（C8）** — `WithSingleProcess()` 一键开关，多 Module 在同一进程通过反射直通通信，**零 Consul 依赖**，适合单元测试和小型部署

### 📊 验证指标

- **265 个单测 / 集成测试**（集成测试以 `//go:build integration` tag 隔离，默认不跑）
- `go test -race -count=1 ./...` 在 **workspace 内**（仓库外 `go.work` 同时检出 tgf/rpcx/rpcx-consul 三仓）全绿
- `go vet` 零告警
- 14 处 pre-existing bug / race 顺带修复

> 上述构建/测试结果在 go.work workspace 内与 `GOWORK=off` 单仓模式下均成立
> （`go.mod` 内置指向同工作区 fork 目录的 path replace，见下文「从源码构建」）。
> 无本机 Consul/Redis/MySQL 的纯单元测试可独立跑通；集成测试需对应外部服务。

---

## 5 分钟快速上手

### 1. 安装

要求 Go **1.24+**。

tgf 依赖的 rpcx / rpcx-consul fork 已迁移为自有 module path
（`github.com/thkhxm/rpcx` / `github.com/thkhxm/rpcx-consul`），由 tgf 的
`go.mod` 直接 `require` 引入——**下游业务工程不再需要手抄任何 replace 块**：

```bash
go get github.com/thkhxm/tgf@latest
```

> ⚠️ 上述命令生效的前提是两个 fork 仓库已发布**携带新 module path 的 tag**
> （即该 tag 树内 `go.mod` 的 `module` 行为 `github.com/thkhxm/rpcx[-consul]`）。
> 该发布动作见路线图 D1；发布前请使用下面「从源码构建」的 workspace 方式接入。

#### 从源码构建（贡献者）

本仓库与 fork 一起放在一个 Go **workspace**（仓库外的 `go.work` 同时 `use`
`tgf/`、`rpcx/`、`rpcx-consul/` 三个目录）。`tgf/go.mod` 另内置两条指向
`../rpcx`、`../rpcx-consul` 的 path replace，因此只要按
[`doc/architecture.md`](doc/architecture.md) 的布局把三个仓库检出到同级目录，
workspace 内与 `GOWORK=off` 单仓模式均可直接构建。

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

    "github.com/thkhxm/tgf/rpc"
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

更多场景见 [`example/`](example/) 目录下的 9 个示例项目。

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
│  ├── WithMethodPolicy (C6) / WithMetrics (B4) / WithTracer   │
│  └── WithHealthCheck (A6)                                    │
└───────────────────────────┬───────────────────────────────────┘
                            │
       ┌────────────────────┴─────────────────────────┐
       │                                              │
┌──────▼─────────────┐          ┌────────────────────▼─────────┐
│  Gateway           │          │  RPC Server                   │
│  ├── TCP / WS/WSS  │          │  ├── rpcx service registry    │
│  ├── KCP+AEAD (A8) │          │  ├── Consul discovery         │
│  ├── IConn (A2)    │          │  ├── localDispatcher (C8)     │
│  └── 登录锁 (A3)    │          │  └── MethodPolicy 管道 (C6)   │
└──────┬─────────────┘          └────────────────────┬──────────┘
       │                                             │
┌──────▼─────────────────────────────────────────────▼──────────┐
│  Data Layer                                                   │
│  ├── autoCacheManager (write-behind, A1 + A1b FailureQueue)  │
│  ├── game_config (C5 hot reload + fsnotify)                  │
│  └── config/ (C3 struct-tag 驱动)                              │
└───────────────────────────────────────────────────────────────┘
```

完整架构说明见 [`doc/architecture.md`](doc/architecture.md)。

---

## 功能清单

### 网关与连接

| 能力 | API | 说明 |
|------|-----|------|
| TCP / WS / WSS / KCP 网关 | `WithGatewayOptions(GatewayOptions{TCPPort, WSPath, WSTLSKey, WSTLSCert, KCP})` | 统一入口，按需组合 |
| KCP + AEAD | `NewKCPBuilder(port).WithAEADKey(key)` | ChaCha20-Poly1305 帧加密 |
| 踢人 / 重复登录 | `loginCoord` 接口 | Redis 锁 + 定向踢远端 |
| 健康心跳 | `WithHealthCheck(interval)` | 骨架版本 |
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
| 热更 | `config.Reload()` + `OnReload(fn)` | 业务侧手动调用触发（框架未内置信号/HTTP 触发器） |
| 游戏配置 | `component.GetGameConf[Val](id)` | 泛型查询 |
| 游戏配置热更 | `component.ReloadGameConf()` + `StartConfigWatcher()` | 手动调用 / fsnotify 目录监听 |

---

## 示例项目

[`example/`](example/) 目录下有 9 个可独立运行的示例，覆盖 tgf v2 的各个模块：

| 目录 | 演示内容 |
|------|---------|
| [`single_process/`](example/single_process/) | 单进程多 Module + 跨 module RPC + 策略 + metrics |
| [`robot_test/`](example/robot_test/) | WS + KCP robot 自测（登录 + 多人移动同步，QPS ~200 万） |
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

大部分示例不需要外部服务（Redis/MySQL 缺失时静默跳过），`go run .` 即可。

---

## 文档

| 文档 | 说明 |
|------|------|
| [`doc/architecture.md`](doc/architecture.md) | v2 完整架构图 + 关键包索引 + 流程说明 |
| [`doc/migration-v1-to-v2.md`](doc/migration-v1-to-v2.md) | v1 → v2 迁移指南 + 升级 checklist |
| [`doc/observability.md`](doc/observability.md) | Prometheus / OpenTelemetry / Loki 接入指南 |
| [`CHANGELOG.md`](CHANGELOG.md) | 完整变更日志 |

### 外部链接

- **API 参考**：[pkg.go.dev/github.com/thkhxm/tgf](https://pkg.go.dev/github.com/thkhxm/tgf)
- **项目地址**：[github.com/thkhxm/tgf](https://github.com/thkhxm/tgf)
- **项目文档**：[thkhxm.github.io/tgf_writerside](https://thkhxm.github.io/tgf_writerside/starter-topic.html)
- **国内文档镜像**：[tgf.yamigame.net:8080](http://tgf.yamigame.net:8080/)
- **示例项目**：[github.com/thkhxm/tgf-tutorial](https://github.com/thkhxm/tgf-tutorial)
- **知乎博客**：[tim-30-83](https://www.zhihu.com/people/tim-30-83/posts)
- **CSDN 专栏**：[tgf 专栏](https://blog.csdn.net/thkhxm/category_12520142.html)
- **B 站教程**：[space.bilibili.com/64497732](https://space.bilibili.com/64497732/channel/seriesdetail?sid=3815364)

---

## 技术选型

**Go 工具链**：Go 1.24+（v2 升级）

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

- ✅ v2-alpha：A / B / C 档落地（稳定性修复 + 工程化 + API 演进），265 个测试
- ⏳ **v3-D 档（进行中）止血与发布可用**：消灭 P0（串包、单进程网关、优雅停机、
  下游可消费、凭据卫生、登录鉴权地基），让框架"对外存在"
- 📅 v3-E 档：接线收尾（策略管道全覆盖、配置系统收敛、可观测性落地、数据层故障路径）
- 📅 v3-F 档：生产化地基（fork 治理、Consul TTL check、会话与踢人收尾、过载保护）
- 📅 v3-G 档：定位对齐（HTTP web 能力，按需启动）
- 📅 更远期：
  - DB 层真正的分库分表（sqlBuilder 重构）
  - OpenTelemetry / Prometheus adapter 官方 subpackage

---

## License

MIT License — 见 [LICENSE](LICENSE)。

---

*tgf v2-alpha（D 档止血中）· 最后更新 2026-06-10*
