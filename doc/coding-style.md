# tgf v2 编码规范

本文是 tgf v2（`github.com/thkhxm/tgf/v2`）的统一编码规范，面向两类读者：

- **业务项目开发者** —— 用 tgf 写游戏服 / web 服 / 工具的人；
- **框架贡献者** —— 直接改 `tgf/` / `rpcx/` / `rpcx-consul/` 的人。

每条规则尽量配一组「正例 / 反例」，所有代码片段使用仓库**真实存在的 API**（可在仓库 Grep 复核）。
凡是"仓库目前还没完全做到、但作为约束推荐"的条目，会显式标注「目标态」。

> 约定：本文出现的 `tgf` 指根包 `github.com/thkhxm/tgf/v2`；`rpc` 指 `.../v2/rpc`；
> `web`、`log`、`config`、`metrics`、`db`、`trace` 同理为对应子包。

---

## 1. 目录结构

### 1.1 框架仓库布局速览

tgf 是一个 Go **workspace**（`go.work`），同仓三模块协同开发：

| 模块 | 作用 |
|------|------|
| `tgf/` | 框架本体 `github.com/thkhxm/tgf/v2`（主产物） |
| `rpcx/` | `smallnest/rpcx` 的 fork，经 `go.mod` replace 为 `github.com/thkhxm/rpcx/v2` |
| `rpcx-consul/` | `rpcxio/rpcx-consul` 的 fork，replace 为 `github.com/thkhxm/rpcx-consul/v2` |

`tgf/` 内部包职责（贡献者需要知道改哪儿）：

```
tgf/
├── define.go          # Environment / Context key / 模块名等常量（唯一真源）
├── config.go          # 旧 GetStrConfig 适配层（读 config 包快照）
├── error.go           # 框架级 sentinel error（DBEmpty / ErrConnClosed ...）
├── init.go            # 包 init：InitConfig + 信号优雅停机
├── config/            # 新配置系统（struct tag 驱动，唯一 parse pass）
├── rpc/               # 服务器 builder、网关、service、HTTP 桥、登录鉴权、策略
│   └── internal/      # 不对外的 consul / discovery / health / http 装配
├── web/               # 一等公民 HTTP（Router / 中间件 / Backend 桥 / 限流）
├── db/                # AutoCacheBuilder 自动缓存（Redis 热 + MySQL 冷 + 补偿队列）
├── log/               # zap + lumberjack 封装
├── metrics/           # Provider 抽象 + memory / prometheus 实现
├── trace/             # traceId 上下文透传
├── component/         # 游戏 JSON 配置加载与热更
├── util/              # 泛型工具（协程池 / Snowflake / 类型转换）
└── example/           # 可独立 go run 的示例（业务写法的权威参考）
```

贡献铁律：bug 在 rpcx 路由 / selector / 传输层时**直接改 `rpcx/`**，不要 `go get` 上游——
`server.WithLogicSync` 等是 fork-only API，升级上游会丢。

### 1.2 业务项目标准布局（推荐）

业务项目按标准 Go 应用布局组织，把「RPC 接口声明」「service 实现」「入口」「配置」分层：

```
myapp/
├── cmd/
│   └── gameserver/
│       └── main.go            # 仅装配 builder + 启动，不写业务逻辑
├── internal/
│   ├── api/                   # ServiceAPI 声明（跨包/跨进程调用的契约）
│   │   ├── user.go            # var UserLogin = &rpc.ServiceAPI[...]{...}
│   │   └── shop.go
│   ├── service/               # IService 实现（内嵌 rpc.Module）
│   │   ├── user.go
│   │   └── shop.go
│   └── model/                 # db.Model 业务数据结构
├── configs/
│   ├── .env.dev               # TGFMODULE=dev 时加载
│   ├── .env.test
│   └── .env.release
├── deploy/                    # Dockerfile / k8s yaml / compose
├── go.mod                     # require github.com/thkhxm/tgf/v2 v2.0.0
└── go.work                    # 本地联调框架时可选（生产用 tag）
```

正例 —— `cmd/gameserver/main.go` 只负责装配与启动：

```go
package main

import (
    "github.com/thkhxm/tgf/v2"
    "github.com/thkhxm/tgf/v2/rpc"
    "myapp/internal/service"
)

func main() {
    <-rpc.NewRPCServer().
        WithCache(tgf.CacheModuleClose).
        WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).
        WithService(service.NewUserService()).
        WithService(service.NewShopService()).
        Run()
}
```

反例 —— 把业务逻辑、handler、SQL 全堆进 `main.go`，service 散落不分层：

```go
// main.go 里直接写 Login 业务、拼 SQL、调 Redis…… 几百行挤一个文件。
// 后果：无法被测试覆盖、无法被另一个进程复用、ServiceAPI 与实现纠缠。
```

`internal/api` 与 `internal/service` 分离的价值：`api` 包是**纯契约**（`ServiceAPI` 描述符 + Req/Res 结构体），
调用方只 import `api` 即可发起 RPC，不必依赖 service 实现，避免循环依赖。

正例 —— `internal/api/user.go`：

```go
package api

import "github.com/thkhxm/tgf/v2/rpc"

type LoginReq struct{ UserId string }
type LoginRes struct{ Welcome string }

// UserLogin 是 user 模块 Login 方法的 RPC 描述符。
var UserLogin = &rpc.ServiceAPI[*LoginReq, *LoginRes]{
    ModuleName:  "user",
    Name:        "Login",
    MessageType: "user.Login",
}
```

`config/` 中三份 `.env.<module>` 由 `TGFMODULE` 环境变量选择（`dev` / `test` / `release`，缺省 `dev`），
框架 `InitConfig()` 启动期用 `godotenv` 从**工作目录**加载，因此容器里把 `.env.release` 放在
启动工作目录即可（或干脆全用环境变量注入，不放文件）。

---

## 2. 命名

### 2.1 包名

包名小写单词、不带下划线、不带复数（与标准库一致）。

正例 / 反例：

```go
package service   // 正
package web       // 正
package userService   // 反：驼峰
package user_api      // 反：下划线
package services      // 反：复数
```

### 2.2 Service 模块名（`rpc.Module{Name}`）

模块名是 RPC 路由 + Consul 注册的逻辑键，用**小写单词**，与 `ServiceAPI.ModuleName` 和
`MessageType` 前缀保持完全一致。

正例：

```go
type UserService struct {
    rpc.Module
}

func NewUserService() *UserService {
    return &UserService{
        Module: rpc.Module{Name: "user", Version: "1.0"},
    }
}
```

对应的 API 描述符 `ModuleName` 必须同名：

```go
var UserLogin = &rpc.ServiceAPI[*LoginReq, *LoginRes]{
    ModuleName:  "user",        // 与 Module{Name:"user"} 一致
    Name:        "Login",
    MessageType: "user.Login",  // 前缀 = ModuleName
}
```

反例（模块名与 API 不一致，路由必然找不到 service）：

```go
Module: rpc.Module{Name: "UserModule"}   // 反：大写 + 后缀
// 而 API 写 ModuleName:"user" → MessageType "user.Login" 永远路由不到
```

### 2.3 ServiceAPI 变量命名

`ServiceAPI` 变量用 `<模块><方法>` 的驼峰式包级变量，放在 `internal/api`，
读起来像「调哪个模块的哪个方法」。

正例：`UserLogin`、`ShopGiveGift`、`ShopBuyItem`（见 `example/single_process/main.go`）。

反例：`loginApi`、`api1`、`GiveGiftServiceAPIDescriptor`（前两个无信息量，后者冗长）。

### 2.4 文件命名

Go 源文件小写、用下划线分词（Go 唯一允许下划线的地方），按「领域」而非「类型」聚合。

| 场景 | 正例 | 反例 |
|------|------|------|
| service 实现 | `user.go` / `shop.go` | `UserService.go`（大写） |
| 测试 | `user_test.go` | `userTest.go` |
| 集成测试 | `it_login_lock_test.go` | 见 §7.2（必须带 build tag） |

框架自身约定：集成测试文件统一 `it_*_test.go` 前缀（仓库现状），便于一眼区分单测与重依赖测试。

---

## 3. 错误处理

### 3.1 禁吞错（最高优先级）

任何返回 `error` 的调用，要么处理、要么用 `%w` 向上包裹，**绝不允许 `_` 丢弃**。
这是 v3 整改最痛的教训：`d, _ := xxx()` 把故障吞掉，导致 DB 故障被当成"无数据"，
用默认存档覆盖了玩家真实存档。

反例（被明令禁止）：

```go
keys, _ := h.loadAll()        // 反：DB 真实故障被静默丢弃
val, _ := cache.Get(uid)      // 反：拿到零值还以为是"查无此人"
```

正例（处理或包裹）：

```go
keys, err := h.loadAll()
if err != nil {
    return fmt.Errorf("loadAll 失败: %w", err)   // %w 保留错误链
}
```

例外：测试代码里的便捷写法允许丢错（`.golangci.yml` 已对 `_test.go` 关闭 `errcheck`）；
生产代码无此豁免。

### 3.2 `%w` 错误链

包裹错误用 `%w`（不是 `%v`），让上层能 `errors.Is` / `errors.As` 还原根因。
框架本身就靠这条把真实故障与"无数据"叠加传递：

正例（`db/manager.go` 的真实写法）：

```go
// 把底层故障包上 ErrDBFault 向上抛，调用方仍可 errors.Is 识别
return []string(nil), fmt.Errorf("%w: %w", ErrDBFault, qerr)
```

反例：

```go
return nil, fmt.Errorf("查询失败: %v", qerr)   // 反：%v 截断错误链，errors.Is 失效
```

### 3.3 区分"故障"与"空"（db 的 ErrDBFault vs DBEmpty 模式）

游戏服最危险的 bug 是把"DB 挂了"误判成"这个玩家不存在"，然后用默认档覆盖真存档。
tgf 用两个 sentinel error 严格区分，业务**必须**沿用同一判定模式：

- `tgf.DBEmpty`（定义于 `tgf/error.go`）—— 数据确实不存在（查无此行）；
- `db.ErrDBFault`（定义于 `db/manager.go`）—— 持久层真实故障（连接断 / 扫描失败）。

正例：

```go
import (
    "errors"
    "github.com/thkhxm/tgf/v2"
    "github.com/thkhxm/tgf/v2/db"
)

data, err := cache.Get(uid)
switch {
case err == nil:
    use(data)
case errors.Is(err, tgf.DBEmpty):
    data = newDefaultProfile(uid)   // 安全：确实是新玩家，给默认档
case errors.Is(err, db.ErrDBFault):
    return err                       // 故障：禁止用默认档覆盖，原样抛上去
default:
    return fmt.Errorf("读取存档异常: %w", err)
}
```

反例（v3 整改前的祸根）：

```go
data, err := cache.Get(uid)
if err != nil {
    data = newDefaultProfile(uid)   // 反：DB 故障也走这里 → 默认档覆盖真存档
}
```

### 3.4 sentinel error 的定义位置

新增框架级哨兵错误放对应包的 `error.go` / `manager.go` 顶部，用 `errors.New`，
导出名带 `Err` 前缀（`ErrConnClosed`、`ErrConnSendTimeout`、`ErrUserNotFound` 见 `tgf/error.go`）。

### 3.5 panic 仅限不可恢复

`panic` 只用于「程序写错了、继续跑没意义」的场景（如装配期非法参数）。
请求处理路径上的可预期错误一律 `return error`，不准 `panic` 当控制流。

反例：

```go
func (s *UserService) Login(ctx context.Context, req *LoginReq, res *LoginRes) error {
    if req.UserId == "" {
        panic("userId 为空")   // 反：一个坏请求打挂整个进程
    }
}
```

正例：

```go
func (s *UserService) Login(ctx context.Context, req *LoginReq, res *LoginRes) error {
    if req.UserId == "" {
        return errors.New("userId 必填")   // 正：坏请求只失败这一次调用
    }
}
```

---

## 4. 日志

tgf 的日志在 `tgf/log` 包，底层 zap + lumberjack。热路径性能是硬约束。

### 4.1 用 `*TagW`（zap.Field 零分配），不要 Sprintf 风格

新代码一律用 `*W` / `*TagW` 系列（`InfoTagW` / `DebugTagW` / `WarnTagW` / `ErrorTagW`），
手写 `zap.String` / `zap.Int` 字段，**完全不碰 `fmt.Sprintf`**，避免 interface 装箱与字符串分配。
老的 `InfoTag(tag, "...%v", x)` Sprintf 风格在 v2 发布后一版会标 deprecated。

正例（`example/single_process/main.go` 的真实写法）：

```go
log.InfoTagW("user", "用户登录",
    zap.String("userId", req.UserId),
    zap.Int("level", level),
)
```

反例：

```go
log.InfoTag("user", "用户登录 userId=%s level=%d", req.UserId, level)
// 反：即使日志级别过滤掉，Sprintf 仍可能执行；且字段进了 message 文本，Loki/ELK 无法按 key 检索
```

### 4.2 链路字段用预置构造器，带 traceId 用 `*TagWT`

traceId / userId / nodeId 等链路关键字段，用 `log` 包预置的零分配构造器
（`log.TraceID` / `log.UserID` / `log.NodeID` / `log.Module` / `log.Method`），
保证 Loki/ELK 侧字段名（`traceId` / `userId` / `nodeId` / ...）全框架一致可聚合。

热路径（网关 / RPC / DB）推荐 `*TagWT` 把 traceId 前置：

正例：

```go
log.InfoTagWT(log.DBTAG, traceId, "落库完成",
    zap.String("db", "tgf"), zap.Int32("count", n))
```

反例：

```go
log.DebugTag("db", "落库完成 trace=%s db=%s count=%d", traceId, "tgf", n)
// 反：traceId 被 Sprintf 进文本，下游只能正则捞
```

### 4.3 tag 规范

tag 是日志分流 + 运行期过滤的键。框架内置三个有专用文件的 tag 常量：
`log.GAMETAG`（"game"）、`log.DBTAG`（"db"）、`log.SERVICETAG`（"service"）。
业务用稳定的小写短词 tag（与模块名对齐，如 `"user"` / `"shop"` / `"gate"` / `"init"`），
便于用 `LogIgnoredTags` 环境变量在运行期屏蔽噪音 tag。

### 4.4 高频路径短路用 `CheckLogTag`（仅老 API 场景）

`*TagW` 系列内部已自带 tag 短路与 level 前置过滤，**优先用它**，无需手动判断。
仅当你不得不用老 Sprintf API 且 Sprintf 本身昂贵时，先 `log.CheckLogTag(tag)` 短路：

```go
if log.CheckLogTag("gate") {
    log.InfoTag("gate", expensiveSprintf())   // 仅在 tag 未被屏蔽时才算 expensiveSprintf
}
```

### 4.5 禁 `fmt.Println` / `fmt.Printf` 打业务日志

业务代码、service、handler 里禁止用 `fmt.Println` / `fmt.Printf` 输出日志——
它们绕过 level 过滤、绕过 tag 分流、不进日志文件、无结构化字段。

反例：

```go
fmt.Printf("用户 %s 登录\n", uid)   // 反：进不了日志系统
```

正例：

```go
log.InfoTagW("user", "用户登录", log.UserID(uid))
```

> 例外：框架 `config.go` / `init.go` 在**日志系统尚未初始化的启动早期**用 `fmt.Printf`
> 打印配置快照是有意为之（此时 logger 还没建好）；业务代码不存在这个早期窗口，无此例外。

---

## 5. 配置

### 5.1 唯一真源：`config.Current()` / `OnReload`，禁 `os.Getenv` 直读

所有运行期配置从 `config.Current()` 读类型化字段，**禁止业务代码裸读 `os.Getenv`**。
配置经 `tgf/config` 包**唯一一次 parse pass**解析（struct tag 驱动），新旧 API 读同一份快照。

正例：

```go
import "github.com/thkhxm/tgf/v2/config"

cfg := config.Current()
addr := cfg.Redis.Addr           // 类型化、有默认值、Reload 后自动是新值
timeout := cfg.RPC.DefaultTimeoutMs
```

反例：

```go
addr := os.Getenv("RedisAddr")   // 反：无默认值、无类型校验、热更不生效、未脱敏
n, _ := strconv.Atoi(os.Getenv("RPCDefaultTimeoutMs"))  // 反：还顺手吞了 err
```

需要响应配置热更的组件，订阅 `config.OnReload`（`config.Reload()` 成功后按注册顺序回调）：

```go
config.OnReload(func(c *config.Config) {
    applyNewRateLimit(c.RPC.DefaultTimeoutMs)   // panic 会被框架 recover，不影响其他订阅者
})
```

业务侧显式加载（如纯 web 进程 main）用 `config.Load()`（严格 fail-fast）：

```go
cfg, err := config.Load()
if err != nil {
    fmt.Printf("配置加载失败: %v\n", err)
    os.Exit(1)
}
```

### 5.2 新增配置项的标准流程

新增一个配置项，**只改 `config/config.go` 一处**——在对应子 struct 上加一个带
`env` + `default` tag 的字段，反射加载器自动生效：

正例（给 `RedisConfig` 加一个新开关）：

```go
type RedisConfig struct {
    Addr     string `env:"RedisAddr" default:"127.0.0.1:6379"`
    Password string `env:"RedisPassword" default:""`
    DB       int    `env:"RedisDB" default:"1"`
    Cluster  bool   `env:"RedisCluster" default:"false"`
    // 新增：直接加字段即可，无需改其它文件
    PoolSize int    `env:"RedisPoolSize" default:"10"`
}
```

支持的字段类型：`string` / `int` 系列 / `uint` 系列 / `float` / `bool`（接受
`1/0/true/false/yes/no/on/off`）/ `time.Duration`（如 `"30s"`）/ `[]string`（逗号分隔）。
必填项加 `required:"true"`。

> 兼容补充：若该配置项还要被老的 `tgf.GetStrConfig[T](tgf.EnvironmentXxx)` 读取，
> 需同时在 `define.go` 加 `Environment` 常量（值 = env key）。
> **目标态**：新代码统一走 `config.Current()`，不再新增 `GetStrConfig` 调用点；
> `GetStrConfig` 仅作存量适配层保留。

反例（v1 旧路径的"改三处"陷阱，已废弃）：

```go
// 反：旧系统要同时改 Environment 常量 + 默认值常量 + initMapping() 登记三处，
//     漏一处就静默错误。v2 已收敛为只改 struct 一处。
```

### 5.3 凭据脱敏

凭据类配置（密钥 / 口令 / 密码）必须登记进 `config.go` 的 `sensitiveEnvKeys`，
启动日志打印快照时会被替换为 `******`。新增凭据类配置项时同步登记：

正例（`tgf/config.go` 现状）：

```go
var sensitiveEnvKeys = map[string]bool{
    string(EnvironmentLoginTokenSecret): true,
    string(EnvironmentAdminToken):       true,
    string(EnvironmentRedisPassword):    true,
    string(EnvironmentMySqlPwd):         true,
    // 新增凭据项 → 在此登记，否则会明文出现在启动日志
}
```

反例：新增了 `ThirdPartyApiKey` 配置但忘了登记 `sensitiveEnvKeys`
→ 启动日志里明文打印密钥，留痕泄露。

---

## 6. builder 与 service 约定

### 6.1 service 方法签名固定 `(ctx, *Req, *Res) error`

所有可被 RPC（或 HTTP 桥）调用的 service 方法，签名固定为
`func (s *T) Method(ctx context.Context, req *Req, res *Res) error`——
rpcx 与本地 dispatcher 都靠这个签名识别。出参写进 `res` 指针，函数只返回 `error`。

正例（`example/single_process/main.go`）：

```go
func (s *ShopService) GiveGift(ctx context.Context, req *GiveGiftReq, reply *GiveGiftRes) error {
    reply.OK = true
    return nil
}
```

反例：

```go
func (s *ShopService) GiveGift(req *GiveGiftReq) (*GiveGiftRes, error) { ... }
// 反：返回值出参 + 缺 ctx，rpcx 无法识别，注册后调用必失败
```

### 6.2 service 实现内嵌 `rpc.Module`

业务 service 内嵌 `rpc.Module{Name, Version}`，自动获得 `IService` 的身份/生命周期默认实现
（`GetName` / `GetVersion` / `Destroy` / `LogicSyncMethods`）和登录/下线钩子、Consul state 通知能力
（满足可选子接口 `IStatefulService` / `IUserLifecycleService`）。

正例：

```go
type UserService struct {
    rpc.Module
}

func NewUserService() *UserService {
    return &UserService{Module: rpc.Module{Name: "user", Version: "1.0"}}
}
```

反例（裸实现 IService 不嵌 Module）：启动时会被 `wireServiceCapabilities` 归入 `PlainOnly`
并打 WARN——它收不到 state 通知、吃不到全局钩子。除非你明确不需要这些能力，否则嵌 `Module`。

### 6.3 `Startup` 职责

`Startup() (bool, error)` 是 service 自己的初始化点：加载本模块所需资源、预热缓存、
注册定时器等。返回 `(true, nil)` 表示启动成功；返回 `error` 或 `false` 让框架知道这个模块没起来。
**不要**在 `Startup` 里写"等待外部信号"的阻塞循环（那会卡死启动链）。

正例：

```go
func (s *UserService) Startup() (bool, error) {
    log.InfoTagW("user", "UserService 启动", zap.String("version", s.Version))
    return true, nil
}
```

### 6.4 LogicSyncMethods（串行化方法）

需要 rpcx 对某些方法**按 userId 串行化**处理（避免同一玩家并发改档）时，
override `LogicSyncMethods() []string` 返回这些方法名；返回 `nil`（默认）即全部走并发路径。

正例：

```go
func (s *UserService) LogicSyncMethods() []string {
    return []string{"SaveProfile", "Pay"}   // 这两个方法对同一玩家串行执行
}
```

> 注意：v2 已把 v1 的 `GetLogicSyncMethod` 重命名为 `LogicSyncMethods`（去 Get 前缀、复数）。
> 从 v1 升级务必改方法名，否则原先的 override 静默失效。

### 6.5 builder 选项顺序：`WithGatewayKCP` 必须在 `WithGatewayOptions` 之后

builder 链大部分选项顺序无关，但有**已知陷阱**：`WithGatewayKCP` 依赖链中已存在
一个 `GateService`（由 `WithGatewayOptions` 创建）才能把 KCP 挂上去。若 `WithGatewayKCP`
排在前面，它找不到 GateService 会**新建一个带默认 TCP 端口的 GateService**，
随后的 `WithGatewayOptions` 又建第二个，导致双网关 / 端口意外。

正例（KCP 在 GatewayOptions 之后）：

```go
key := [32]byte{ /* ... */ }
kcp := rpc.NewKCPBuilder("8300").WithAEADKey(key[:])

rpc.NewRPCServer().
    WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).  // 先建 GateService
    WithGatewayKCP(kcp).                                      // 再把 KCP 挂上去
    WithService(NewUserService()).
    Run()
```

反例：

```go
rpc.NewRPCServer().
    WithGatewayKCP(kcp).                                      // 反：此时还没有 GateService
    WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).  // 又建一个 → 双网关
    Run()
```

> 也可以直接走 `GatewayOptions.KCP` 字段一次性配齐，避免顺序问题：
> `WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082", KCP: kcp})`。

### 6.6 builder 收尾与启动模式

builder 链以 `.Run() <-chan bool` 结尾，`Run()` 返回的 channel 在停机时关闭。
进程级优雅停机由框架包 init 自动装好（捕获 SIGTERM/SIGINT → Consul 摘除 → 停 accept →
drain → write-behind 终末落库），业务**不需要**自己写信号处理来触发停机；
若想在停机前做收尾，监听 `Run()` 返回的 channel 即可。

按部署形态选启动开关（互斥，选一个）：

| 开关 | 适用 |
|------|------|
| `WithSingleProcess()` | 单进程多 module，零 Consul，进程内 RPC 直通 |
| `WithStandalone()` | 纯 web 进程，不挂 Consul、不启动 RPC client watch |
| `WithClientOnly()` | 纯接入层（如独立 HTTP 进程接后端游戏服），不注册 service、不监听 rpcx 端口，但保留 Consul discovery + RPC client |
| （默认） | 标准分布式游戏服节点，注册 service 进 Consul |

正例（纯 REST 进程，`example/http_rest/main.go`）：

```go
<-rpc.NewRPCServer().
    WithStandalone().
    WithCache(tgf.CacheModuleClose).
    WithHTTPService(web.Options{ /* ... */ }).
    Run()
```

### 6.7 HTTP handler 经 `web.Backend` 桥调后端，不绕过策略管道

HTTP 服务里要调后端 module 时，从请求取 `web.BackendFromRequest(r)` 再 `Invoke`，
让调用复用同一条策略管道（限流/熔断）+ 可观测埋点 + traceId 透传，而不是自己拼 rpcx 调用。

正例（`example/http_rpc/main.go`）：

```go
backend, ok := web.BackendFromRequest(r)
if !ok {
    http.Error(w, "backend 未注入", http.StatusInternalServerError)
    return
}
args := GetPlayerReq{PlayerId: r.PathValue("id")}
var reply GetPlayerRes
if err := backend.Invoke(r.Context(), "player", "GetPlayer", &args, &reply); err != nil {
    http.Error(w, err.Error(), http.StatusBadGateway)
    return
}
```

---

## 7. 测试

### 7.1 表驱动 + `-race` 必过

单元测试用表驱动（`tests := []struct{...}` + `t.Run(tt.name, ...)`），与仓库现状一致
（见 `util/string_test.go`）。提交前 `go test -race ./...` 必须全过——框架大量用 goroutine，
data race 是硬故障。

正例（仓库 `util/string_test.go` 的真实结构）：

```go
func TestStrToAny(t *testing.T) {
    type args struct{ a string }
    type testCase[T any] struct {
        name    string
        args    args
        want    T
        wantErr bool
    }
    tests := []testCase[StringDemoType]{
        {name: "正常 JSON", args: args{validJSON}, want: *want, wantErr: false},
        {name: "非法 JSON", args: args{"{"}, wantErr: true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := util.StrToAny[StringDemoType](tt.args.a)
            if (err != nil) != tt.wantErr {
                t.Errorf("StrToAny() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if !reflect.DeepEqual(got, tt.want) {
                t.Errorf("StrToAny() got = %v, want %v", got, tt.want)
            }
        })
    }
}
```

跑法：

```bash
go test ./...                 # 全部单测
go test -race ./rpc           # 单包带竞态检测
go test ./db -run TestCacheManager   # 单个测试
```

### 7.2 依赖外部服务的测试加 `//go:build integration` + testcontainers

需要真实 Redis / MySQL / Consul 的测试**必须**加 `//go:build integration` build tag，
默认 `go test ./...` 不跑它们（无依赖也能绿）；真实依赖用 `testcontainers-go` 起容器，
不是 mock。文件名按仓库约定用 `it_*_test.go` 前缀。

正例（仓库 `db/it_env_test.go` 的真实头部）：

```go
//go:build integration
// +build integration

package db

// 通过 testcontainers-go 起真实 redis:7-alpine / mysql:8.0 容器，
// Docker 不可用时 t.Skip（CI 的 integration job 带 Docker，必跑）。
```

跑集成测试：`go test -tags=integration ./db`。

反例（重依赖测试不带 tag）：

```go
package db   // 反：没 build tag，普通 go test 就会去连 Redis，无依赖环境直接挂
func TestRealRedis(t *testing.T) { /* 直接 dial 127.0.0.1:6379 */ }
```

### 7.3 禁 `select{}` 挂死

测试里禁止用 `select{}` / 无超时的 `<-ch` 永久阻塞来"等服务起来"——会让 CI 卡死直到超时。
等待用带超时的轮询或 `context.WithTimeout`。

反例：

```go
func TestServer(t *testing.T) {
    go startServer()
    select{}   // 反：永久挂死，CI 直接超时杀进程
}
```

正例：

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := waitReady(ctx, addr); err != nil {
    t.Fatalf("服务未在超时内就绪: %v", err)
}
```

---

## 8. 注释与格式

### 8.1 中文注释

注释、文档、commit message 用中文；标识符（变量名 / 函数名 / API 名 / env key）保持英文。
导出符号的文档注释遵循 Go 惯例（以符号名开头）。

正例：

```go
// GenerateLoginToken 用当前配置的密钥为 userId 签发一个 ttl 后过期的登录 token。
// 业务登录服务在完成账号验证后调用，把返回值下发给客户端。
func GenerateLoginToken(userId string, ttl time.Duration) (string, error) { ... }
```

反例：

```go
// gen token   ← 反：英文碎句 + 不以符号名开头
func GenerateLoginToken(...) {}
```

### 8.2 文件头注释块风格

框架包内文件沿用统一的文件头注释块（`@Link` / 作者 / 日期），新增框架文件**匹配同包风格**：

```go
//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************
```

业务项目无需照搬这个头；用一句话的包/文件用途注释即可（如 `example/` 里每个 main.go 的开头说明）。

### 8.3 gofmt 强制 + linter 门槛

提交前 `gofmt -w .` + `go vet ./...` 必须干净。仓库用 golangci-lint，
当前开启门槛级 linter（`govet`（含 `shadow` / `nilness`）/ `staticcheck` / `errcheck` /
`ineffassign` / `unused` / `gosimple` / `typecheck` / `misspell`），**新代码不得引入新告警**。

```bash
gofmt -w .
go vet ./...
golangci-lint run        # 新代码零告警
```

> 已知豁免（`.golangci.yml`）：`common/` / `util/generated/` / `exp/` 目录与 `*.pb.go`
> 生成代码不检查；`_test.go` 关闭 `errcheck` / `unused`。这些是存量豁免，不是给新代码开的口子。

### 8.4 UTF-8 无 BOM

所有 `.go` 源文件用 **UTF-8 不带 BOM**（含中文注释也一样）。
带 BOM 会让部分工具把 BOM 当源码首字节、`gofmt` 报错。Windows 上用编辑器/脚本保存时
确认无 `EF BB BF` 前缀。

---

## 附：常用 API 速查（均来自仓库真实代码）

| 用途 | API |
|------|-----|
| 构建服务器 | `rpc.NewRPCServer()...Run() <-chan bool` |
| 启动模式 | `WithSingleProcess()` / `WithStandalone()` / `WithClientOnly()` |
| 网关 | `WithGatewayOptions(rpc.GatewayOptions{TCPPort, WSPath, WSTLSKey, WSTLSCert, KCP})` |
| KCP | `rpc.NewKCPBuilder(port).WithAEADKey(key[:])` + `WithGatewayKCP(kcp)` |
| 缓存档位 | `WithCache(tgf.CacheModuleClose)` / `WithCache(tgf.CacheModuleRedis)` |
| 方法策略 | `WithMethodPolicy("mod.Method", rpc.MethodPolicy{Timeout, RateLimit, MaxConcurrency, CircuitBreaker})` |
| HTTP | `WithHTTPService(web.Options{Addr, Routes, Limiter, Middlewares, ShutdownTimeout})` |
| HTTP+Consul | `WithHTTPServiceConsul(opt, rpc.HTTPRegistration{ServiceName, ...})` |
| 指标 / 健康 | `WithMetrics(metrics.NewMemoryProvider())` / `WithHealthCheck(d)` |
| 登录鉴权 | `WithLoginTokenSecret(s)` / `WithoutLoginCheck()` |
| RPC 描述符 | `var API = &rpc.ServiceAPI[*Req,*Res]{ModuleName, Name, MessageType}` |
| 发起 RPC | `rpc.SendRPCMessage(ctx, API.NewRPC(req))` |
| 登录 token | `rpc.GenerateLoginToken(uid, ttl)` / `rpc.UserLoginWithToken(ctx, uid, token)` |
| 全局钩子 | `rpc.RegisterGlobalLoginHook(fn)` / `rpc.RegisterGlobalOfflineHook(fn)` |
| HTTP 桥 | `web.BackendFromRequest(r)` → `backend.Invoke(ctx, module, method, &args, &reply)` |
| HTTP 鉴权/限流 | `web.Auth(web.StaticBearerToken(t))` / `web.NewTokenBucketLimiter(qps)` |
| 配置 | `config.Current()` / `config.Load()` / `config.Reload()` / `config.OnReload(fn)` |
| 日志 | `log.InfoTagW(tag, msg, zap.String(...))` / `log.InfoTagWT(tag, traceId, msg, ...)` |
| 错误判定 | `errors.Is(err, tgf.DBEmpty)` / `errors.Is(err, db.ErrDBFault)` |
| 自动缓存 | `db.NewAutoCacheBuilder[K, *V]().WithMemCache(sec).New()` |

更完整的可运行写法见 `example/` 各子目录（每个都可独立 `go run .`）。
