---
name: tgf-server-dev
description: >
  基于 tgf v2（github.com/thkhxm/tgf/v2，Go 分布式游戏服务器框架）通过"问答→一键生成→自验收"
  搭建与开发业务项目的工作流与规范。
  当用户要：创建 tgf 项目 / tgf 脚手架 / 搭游戏服务器（Go）/ 分布式游戏服 / 单进程小游戏服务端 /
  Go 游戏框架选型用 tgf / 用 tgf 写 http web 服务（REST API）/ HTTP+RPC 混合服务 /
  接 TCP、WebSocket、KCP 长连接网关 / tgf 数据层（Redis/MySQL write-behind）/
  tgf 部署（docker-compose、K8s）/ 排查 tgf 框架问题并提 issue 时使用。
---

# tgf-server-dev — tgf v2 业务项目脚手架与开发规范

## 1. 框架事实速查（写代码前先对齐）

| 事实 | 值 |
|------|----|
| module path | `github.com/thkhxm/tgf/v2`（v2 起带 /v2 主版本后缀） |
| 业务项目依赖 | `require github.com/thkhxm/tgf/v2 v2.0.0`，**不需要任何 replace**（fork 依赖 `thkhxm/rpcx/v2 v2.0.3`、`thkhxm/rpcx-consul/v2 v2.0.2` 由 tgf 自动带入） |
| Go 工具链 | `go 1.26.0` / `toolchain go1.26.4` |
| 常用 import | `"github.com/thkhxm/tgf/v2"`（根包常量如 `tgf.CacheModuleClose`）、`/v2/rpc`、`/v2/web`、`/v2/config`、`/v2/log`、`/v2/db`、`/v2/metrics`、`/v2/trace`、`/v2/robot` |
| 入口 builder | `rpc.NewRPCServer().With*(...).Run()`，`Run()` 返回 `<-chan bool` 阻塞至停机 |
| 配置 | `.env.<TGFMODULE>`（dev/test/release），统一经 `config.Current()` 读 |
| 停机 | SIGTERM/SIGINT 自动优雅停机（Consul 摘除→停 accept→drain→write-behind 终末落库），容器/K8s 直接可用 |
| 安全默认 | 登录鉴权 fail-closed（必配 `LoginTokenSecret`）；admin 控制面 fail-closed（`ADMIN_TOKEN`） |
| 真实示例 | tgf 仓库 `example/single_process`、`example/http_rest`、`example/http_rpc`、`example/db_cache`、`example/robot_test` |

API 详表（builder 选项全集 / web 包 / 数据层 / 登录鉴权 / robot 客户端）见
[references/api-reference.md](references/api-reference.md)。**写任何框架调用前不确定就查它，
再不确定就读 tgf 源码——禁止凭记忆杜撰 API。**

## 2. 工作流总览

```
①问答(AskUserQuestion 收集 5 个决策点)
  → ②选模板 + 替换占位符生成项目
  → ③go mod tidy → build → run 冒烟
  → ④对照自验收清单逐项打钩（不过就修，直到全过）
  → ⑤交付给用户（附运行方式 + 下一步建议）
```

## 3. 问答决策树（用 AskUserQuestion 逐项问）

用户已在指令里说清的项不要重复问；没说清的**必须问**，不要猜。

**Q1 场景形态**（决定用哪套模板）
- ① 单进程游戏服 —— 开发/小游戏/原型，零外部依赖 → `templates/single_process`
- ② 分布式游戏服 —— 网关 + 多业务进程 + Consul → `templates/distributed`
- ③ 常规 HTTP web 服务 —— 纯 REST（可 client-only 接入层）→ `templates/http_rest`
- ④ 混合 —— HTTP 入口 + 游戏服 RPC（HTTP→RPC 桥）→ `templates/http_rpc`

**Q2 数据层**（决定 WithCache 与 .env 注释开关）
- ① 无持久化 → `WithCache(tgf.CacheModuleClose)`，零外部依赖
- ② Redis 缓存 → `WithCache(tgf.CacheModuleRedis)` + `.env` 打开 `RedisAddr`
- ③ Redis + MySQL write-behind → 同②再打开 `MySql*`；数据模型接
  `db.AutoCacheBuilder`（`WithAutoCache` + `WithLongevityCache` 周期落库 +
  `WithLongevityFailureQueue` 补偿队列——落库失败入队、`db.ReplayFailureQueue`
  启动重放，优雅停机做终末 flush，保证不丢脏数据）
- 注：选 Q1② 分布式时网关本身就依赖 Redis（用户→节点路由表），Q2 最低也是②。

**Q3 客户端接入协议**（仅 Q1①②④ 游戏形态问）
- TCP（默认）→ `GatewayOptions{TCPPort: "8082"}`
- WebSocket（H5/小游戏）→ 加 `WSPath: "/ws"`（WSS 再配 `WSTLSKey/WSTLSCert`）
- KCP（实时同步/帧同步）→ 加 `KCP: rpc.NewKCPBuilder("8300").WithAEADKey(key32)`，
  并追问 Q5 的 AEAD 密钥项

**Q4 部署目标**（决定拷哪些 deploy 模板）
- ① 本地直跑 → 不拷 deploy/（或只留 README 说明）
- ② docker-compose → 拷 `deploy/docker-compose.yml` + `deploy/Dockerfile`
  （Redis/MySQL/Consul 服务编排齐全，按 Q1/Q2 裁剪不需要的服务）
- ③ K8s → 再拷 `deploy/k8s.yaml`（SIGTERM 优雅停机 / Consul TTL 死节点自动摘除
  已内建，无需 preStop 钩子；`terminationGracePeriodSeconds` 已留 30s drain 时间）

**Q5 安全项**（不问用户要不要，**默认就要**；只问值从哪来）
- `LoginTokenSecret`：网关登录鉴权 HMAC 密钥，**默认 fail-closed——不配则
  gate.Login 拒绝所有登录**。生成项目时直接帮用户生成随机值填入 `.env.dev`
  （生产环境提醒换新值并走密钥管理）；
- `ADMIN_TOKEN`：admin 控制面 / HTTP admin 分组口令（不用可留注释，fail-closed 503）；
- AEAD key（仅选 KCP 时）：32 字节预共享密钥；不设即明文（仅限开发/内网），
  长度非 32 字节启动期 panic（fail-fast）。

## 4. 模板与占位符

```
templates/
├── single_process/   # Q1① 单 main：user+shop 模块 + TCP 网关 + 进程内冒烟
├── distributed/      # Q1② 双 main：cmd/gateway + cmd/game + internal/{api,service}
├── http_rest/        # Q1③ 单 main：REST 路由/限流/Bearer 鉴权/traceId
├── http_rpc/         # Q1④ 单 main：HTTP→RPC 桥 + 方法策略 +《拆分多进程》指引
└── deploy/           # Q4：Dockerfile（多阶段 go1.26）/ docker-compose.yml / k8s.yaml
```

每套含 `main.go`（真实可编译代码）、`go.mod.tmpl`、`.env.dev`、`README.md`。

**占位符替换规则**（生成时对拷出的**所有文件**做文本替换）：

| 占位符 | 替换为 | 出现位置 |
|--------|--------|---------|
| `{{PROJECT_NAME}}` | 模块路径/项目名（如 `mygame` 或 `github.com/xx/mygame`；distributed 模板因 import 内部包，**必须是合法 Go module path**） | go.mod.tmpl、.go、.env、README、deploy |
| `{{LOGIN_TOKEN_SECRET}}` | 生成的随机长字符串（≥32 字符） | .env.dev |
| `{{ADMIN_TOKEN}}` | 生成的随机口令（不用 admin 则连同该行保持注释） | .env.dev |
| `{{AEAD_KEY_HEX}}` | 64 位 hex（32 字节；仅 KCP） | distributed/.env.dev |

替换后把 `go.mod.tmpl` 重命名为 `go.mod`。随机值生成（PowerShell）：

```powershell
-join ((48..57)+(97..122) | Get-Random -Count 48 | ForEach-Object {[char]$_})   # secret/token
-join ((0..63) | ForEach-Object { '{0:x}' -f (Get-Random -Maximum 16) })        # 32 字节 hex
```

## 5. 一键生成步骤（AI 照此执行）

```powershell
# ① 建项目目录并拷模板（<tpl> 取 Q1 答案对应目录）
New-Item -ItemType Directory -Force <项目路径>
Copy-Item -Recurse <skill>/templates/<tpl>/* <项目路径>/
# Q4 选②③时：
Copy-Item -Recurse <skill>/templates/deploy <项目路径>/deploy

# ② 按 §4 替换全部占位符；go.mod.tmpl → go.mod
# ③ 按 Q2/Q3 答案调整 main.go（WithCache 档位 / GatewayOptions 协议）与 .env.dev 注释开关
# ④ 写 .gitignore（至少：.env.* 、log/ 、可执行产物）；git init（用户没反对时）

# ⑤ 构建
go mod tidy
go build ./...

# ⑥ 运行冒烟（按模板 README 的"运行/验证"一节逐条执行并核对预期输出）
```

各模板冒烟预期（详见各自 README）：

| 模板 | 启动命令 | 必须看到 |
|------|---------|---------|
| single_process | `go run .` | 启动横幅 + `[冒烟] Login 成功: 欢迎 player_001，新手礼包已发放！` |
| http_rest | `go run .` | `curl /health`→200；`/users/42`→200 JSON；`/admin/stats` 无口令 401、带口令 200 且 `traceId` 非空 |
| http_rpc | `go run .` | `curl /players/p001`→`{"PlayerId":"p001","Level":1}`；POST gift→`OK:true`；`X-Trace-Id` 请求头原样回写 |
| distributed | `docker compose up -d consul redis` 后分别 `go run ./cmd/game`、`go run ./cmd/gateway` | 两进程启动横幅；game 日志出现 `UserService 启动` 与 discovery 节点注册；无 panic |

**自验收清单（全过才许交付给用户；任一不过 → 修复 → 重跑）**：

- [ ] `go mod tidy` 干净通过，go.mod 无任何 replace、无手抄 fork 依赖
- [ ] `go build ./...` 与 `go vet ./...` 零错误
- [ ] 冒烟运行通过（上表对应行全部核对；HTTP 形态必须真 curl，不许只看启动日志）
- [ ] `Ctrl+C`/SIGTERM 后进程优雅退出（日志可见 drain/停机序列，非 panic）
- [ ] `.env.dev` 安全项已按 Q5 落实：LoginTokenSecret 已填随机值（或用户明确知晓暂缺的后果）
- [ ] `.gitignore` 已忽略 `.env.*`，敏感值未进入任何将被提交的文件
- [ ] README.md 与生成结果一致（端口、命令、路由没有复制残留）

## 6. 业务项目目录结构规范

单 main 小项目可平铺；**业务增长后（≥2 个 module 或 ≥2 个进程）必须收敛到标准布局**
（distributed 模板即此布局）：

```
<project>/
├── cmd/<进程名>/main.go     # 每个可执行进程一个目录（gateway/game/web…），main 只做装配
├── internal/api/            # RPC 契约：出入参 + ServiceAPI 描述符（调用方/实现方共同依赖，防 import 环）
├── internal/service/        # Module 实现（rpcx service），一个 module 一个文件
├── internal/dao/            # 数据访问：db.AutoCacheBuilder 封装（有持久化时）
├── configs/                 # 游戏数值 JSON（WithGameConfig 加载）等非敏感配置
├── deploy/                  # Dockerfile / docker-compose.yml / k8s.yaml
├── .env.dev / .env.test / .env.release   # 环境配置（敏感项不入库）
└── go.mod
```

命名、注释、错误处理等编码规范以 tgf 仓库 `doc/coding-style.md` 为准（业务项目同样适用）。

## 7. 安全配置（默认 fail-closed，逐项核对）

| 项 | 不配的后果 | 配置点 |
|----|-----------|--------|
| `LoginTokenSecret` | gate.Login **拒绝所有登录**并打 Error 日志 | `.env.<module>` 或 `WithLoginTokenSecret(s)`；签发 `rpc.GenerateLoginToken(uid, ttl)`，登录 `rpc.UserLoginWithToken(ctx, uid, token)` |
| `ADMIN_TOKEN` | admin 控制面 / `web.BearerToken` 读它的分组 **503** | `.env.<module>`（`config.Current().Security.AdminToken`） |
| KCP AEAD key | KCP 流量**明文**（仅限开发/内网） | `NewKCPBuilder(port).WithAEADKey(key32)`；长度≠32 启动 panic |
| `WithoutLoginCheck()` | 信任客户端自报 userId（任意冒充） | **仅限内网联调显式使用，生产严禁** |

热轮换：改 `.env` 文件后调 `config.Reload()`（或 admin `POST /config/reload`），
密钥/口令对新请求即时生效。

## 8. 部署要点

- **docker-compose**：`deploy/docker-compose.yml` 含 Redis/MySQL/Consul 编排与健康检查；
  容器内互联地址用服务名经环境变量覆盖（`RedisAddr=redis:6379` 等），敏感项经
  `${LOGIN_TOKEN_SECRET:?}` 强制注入，不烧进镜像；
- **Dockerfile**：多阶段构建（golang:1.26-alpine → alpine），`--build-arg MAIN_PKG=./cmd/xxx`
  选进程，`CGO_ENABLED=0` 静态产物；
- **K8s**：`deploy/k8s.yaml` 直接可用——SIGTERM 优雅停机与 Consul TTL 死节点自动摘除
  是框架内建行为，无需 preStop；HTTP 形态开 `/health` 探针，纯 TCP 网关改 tcpSocket 探针；
  密钥走 Secret。

## 9. 框架 bug 一键提交 issue

用户在使用中发现**框架本身**的 bug（非业务代码问题）时，AI 主动走以下流程：

1. **先定位归属**：能在业务侧修的（用错 API、配置缺失）直接修，不提 issue；
   确认是框架缺陷（对照 tgf 源码/文档行为不符、panic 在框架栈内）才提。
2. **收集诊断信息**（字段对齐 tgf 仓库 `.github/ISSUE_TEMPLATE` 的 bug 模板）：
   - tgf 版本（`go list -m github.com/thkhxm/tgf/v2`）与 `go version`
   - 部署形态（单进程/分布式/HTTP；操作系统）
   - **最小复现**：剥离业务后能稳定触发的最小 main.go + 复现步骤
   - 期望行为 vs 实际行为；完整错误栈 / 关键日志段
3. **脱敏（硬规则）**：提交前剔除一切凭据与业务敏感信息——LoginTokenSecret、
   ADMIN_TOKEN、AEAD key、Redis/MySQL 密码、内网 IP/域名、真实玩家数据、
   业务专有逻辑。日志里的密钥即使已打码也不要整段贴。
4. **提交**（确认 `gh --version` 可用且 `gh auth status` 已登录）：

   ```bash
   gh issue create -R thkhxm/tgf \
     --title "[bug] <一句话现象>" \
     --body "<按模板字段组织的正文（中文）>"
   ```

5. gh 不可用或未登录：把组织好的正文交给用户，附手动提交链接
   `https://github.com/thkhxm/tgf/issues/new/choose`。

## 10. 反模式黑名单（硬禁止）

1. **禁 `os.Getenv` 直读框架配置项**（Redis/MySQL/Consul/HTTP/Security 等已登记项）——
   一律 `config.Current().Xxx`；否则 `config.Reload()` 热更对你失效、双轨解析漂移。
   业务自有键不在 Config struct 内时方可读 env（godotenv 已注入），且应集中封装。
2. **禁吞错**：RPC/DB/桥调用的 error 必须处理或带日志向上抛；
   `_ = err`、空 `if err != nil {}` 一律不许。
3. **禁不配 `LoginTokenSecret` 上线**：默认 fail-closed 会拒绝全部登录——上线前必配，
   且不许为"先能跑"改用第 4 条的 `WithoutLoginCheck()` 顶上。
4. **禁生产使用 `WithoutLoginCheck()`**：等于允许任意玩家冒充任意账号；仅限内网联调。
5. **禁 go.mod 手抄 replace 指向 fork**（`thkhxm/rpcx` 等）：tgf 的 require 已自动带入
   fork 正确版本；手抄 replace 会钉死旧版、破坏升级。
6. **禁用 PATH 里的旧版 go 构建**：必须满足 `go ≥ 1.26`（`go version` 先验证）；
   旧工具链对 `go 1.26.0` 指令直接报错或行为偏移。
7. **禁混用新老网关入口**：统一 `WithGatewayOptions`；与 Deprecated 的
   `WithGateway/WithGatewayWS/WithGatewayWSS` 混用会生成多个 GateService 竞争用户表。
8. **禁把客户端可达方法写成普通 struct 参数**：网关帧直达的方法必须
   `rpc.Args[T]/rpc.Reply[T]`（T 为 protobuf Message）；普通 struct 只能服务间
   `SendRPCMessage` 调用——写错了不报编译错误，运行期才发现路由不到。
9. **禁端口冲突装配**：客户端网关端口（`GatewayOptions.TCPPort`）与 rpcx 内部端口
   （`ServicePort`）必须错开；同机多业务实例用 `WithRandomServicePort(min, max)`。
10. **禁敏感值入库/入镜像**：`.env.*` 进 `.gitignore`；密钥经部署环境/Secret 注入。
11. **禁跳过冒烟交付**：`go build` 通过 ≠ 能运行。§5 自验收清单全过才许交付——
    HTTP 形态必须真 curl，游戏形态至少跑通进程内冒烟或 robot 连接。
