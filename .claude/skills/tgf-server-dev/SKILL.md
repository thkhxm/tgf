---
name: tgf-server-dev
description: >
  基于 tgf v2（github.com/thkhxm/tgf/v2）用确定性的 tgfctl 工作流创建、验证和维护 Go 游戏服务器项目。
  当用户要创建 tgf 项目、生成 tgf 脚手架、搭建单进程或分布式游戏服、HTTP REST、HTTP+RPC、
  TCP/WebSocket/KCP 网关、Redis/MySQL write-behind、Docker Compose/K8s 部署，或排查 tgf 使用问题时使用。
---

# tgf-server-dev

用“问答决策 → `tgfctl` 原子生成 → 来源验证 → 构建/冒烟 → 有界修复”交付一致的业务项目。
模板的唯一权威来源是 `github.com/thkhxm/tgf/v2/cmd/tgfctl` 内嵌资源；禁止由模型复制或临场改写模板。

## 1. 先确认工具链与事实

- Go 必须满足 `go 1.26.0`，推荐 `toolchain go1.26.4`；先执行 `go version`。
- 业务依赖必须是 `github.com/thkhxm/tgf/v2` 的公开版本，不手写 rpcx/rpcx-consul，也不加本地 `replace`。
- `@latest` 是默认且受支持的输入；`tgfctl` 必须先解析它，再把精确版本写入 `go.mod`。
- 正常项目可以使用自己的 `go.work`。不要全局或无条件设置 `GOWORK=off`；它只适合发布隔离检查。
- 框架统一处理 `SIGINT/SIGTERM`。业务 `main` 只等待 `Run()` 返回，禁止再写 `signal.Notify` 抢占信号。

框架调用不确定时，先读 [API 速查](references/api-reference.md)，再读对应版本源码，禁止凭记忆杜撰。

## 2. 收集生成参数

用户已说明的项不要重复问；缺少会改变产物的项才询问。

| 决策 | 值 | 约束 |
| --- | --- | --- |
| module | 合法 Go module path | 分布式项目内部 import 依赖它 |
| profile | `single` / `distributed` / `http-rest` / `http-rpc` | 四种一等项目形态 |
| data | `none` / `redis` / `redis-mysql` | `distributed` 最低为 `redis` |
| protocol | `none` / `tcp` / `websocket` / `kcp` / `all` | HTTP 两种 profile 必须为 `none`；游戏网关始终含 TCP，其他值增加 WS/KCP |
| deploy | `local` / `compose` / `k8s` | `compose`、`k8s` 都生成 Dockerfile |
| security | `dev-generated` / `external` | 前者用 CSPRNG 写本地 `.env.dev`；后者只留外部注入占位 |

安全默认：游戏网关必须有 `LoginTokenSecret`；admin 不用时保持 fail-closed；KCP 必须使用 32 字节 AEAD key。
不要让用户在聊天、命令行参数或 Git 文件中提供真实密钥。

## 3. 调用生成器

优先使用 PATH 中的 `tgfctl`。未安装时使用公开模块运行。
已安装工具的入口是 `tgfctl init ...`；下面的 `go run` 形式与它等价：

```bash
go run github.com/thkhxm/tgf/v2/cmd/tgfctl@latest init \
  --module github.com/acme/mygame \
  --dir ./mygame \
  --profile single \
  --data none \
  --protocol tcp \
  --deploy local \
  --security dev-generated \
  --tgf-version latest
```

仅框架维护者测试尚未发布的 CLI 时，才可从明确的 tgf checkout 执行 `go run ./cmd/tgfctl`；
生成项目本身仍必须是 GitHub module consumer，并通过下一节的来源检查。

生成器默认执行 `go mod tidy`、`go build ./...`、`go vet ./...` 和来源验证。目标目录默认必须不存在；
`--force` 也只接受空目录，绝不合并或删除用户文件。`--git-init` 必须由用户明确要求才传。

完整命令、参数交叉约束、原子生成和退出行为见
[生成器契约](references/generator-contract.md)。

## 4. 强制验证依赖来源

生成完成后再次运行：

```bash
tgfctl verify --dir ./mygame
```

如果使用 `go run`，保持同一来源：

```bash
go run github.com/thkhxm/tgf/v2/cmd/tgfctl@latest verify --dir ./mygame
```

通过条件：

- `go.mod` 直接 `require github.com/thkhxm/tgf/v2 v2.x.y`，版本非 `latest`、非空；
- `go.mod` 不含指向 tgf/rpcx/rpcx-consul 的本地 `replace`；
- `go list -m -json github.com/thkhxm/tgf/v2` 有固定 `Version`，没有 `Replace`，也不是 workspace 主模块；
- 当前项目自己的 `go.work` 可以存在，但不能 `use` 本地 tgf 源码或把 tgf 替换成本地目录。

发现父目录 workspace 污染时，移动到该 workspace 之外重新生成或修正项目 workspace；不要把
`GOWORK=off` 写成所有项目的永久规则。

## 5. 项目统一规范

- `cmd/<process>/main.go` 只做装配；多进程或多 module 项目使用 `internal/api`、`internal/service`、
  `internal/dao` 分层。
- 服务只有在 `Startup() == (true, nil)` 时才可被发现/本地分发；失败服务仍由框架清理。
- 服务间 RPC 用 `ServiceAPI` 和指针 reply；客户端帧直达使用 `rpc.Args[T]` / `rpc.Reply[T]`。
- 统一 `WithGatewayOptions`，不与 deprecated `WithGateway*` 混用；网关端口与 rpcx 内部端口错开。
- 配置统一从 `config.Current()` 读取；框架配置禁止散落 `os.Getenv`，否则热更新失效。
- RPC、数据库、HTTP bridge 错误必须处理；禁止 `_ = err`、空错误分支和把上游敏感错误原样返回客户端。
- `.env.dev`、`.env.test`、`.env.release`、日志和可执行产物必须被 `.gitignore` 忽略；
  只提交 `.env.example`，生产密钥走 Secret/环境注入。
- `WithoutLoginCheck()` 只允许明确的内网联调，生产禁止。
- 数据层选 `redis-mysql` 时使用 write-behind 失败队列和启动重放；外部 Redis/MySQL 未实际运行时，
  只能报告“未验证”，不能当作通过。

目录、编码、安全、数据与部署细则见 [项目规范](references/project-standards.md)。

## 6. 场景覆盖

四个 profile 覆盖项目骨架；其余能力作为受测扩展加入，不能另起不一致模板。

| 场景 | 入口 |
| --- | --- |
| 单进程游戏服 | `profile=single` |
| 分布式 gateway + game | `profile=distributed` |
| HTTP REST | `profile=http-rest` |
| HTTP → RPC | `profile=http-rpc` |
| TCP / WebSocket / KCP | 游戏 profile 的 `protocol` |
| Redis / MySQL write-behind | `data`，并按项目规范接 DAO |
| Docker Compose / K8s | `deploy` |
| config reload / game config / logging / metrics+trace / RPC policy / robot / util / platform SDK | 先生成最近的 profile，再按 [场景指南](references/scenario-guide.md) 和框架 `example/` 加入 |

## 7. 验收与有界修复

先保存基线，再执行：

```bash
go mod tidy
go test ./...
go build ./...
go vet ./...
tgfctl verify --dir .
```

然后按 profile 做真实冒烟：HTTP 必须实际请求 `/health` 和业务路由；游戏服至少启动并验证端口/robot；
分布式、Redis/MySQL、Consul 只有在依赖真实启动后才能标记 integration 通过。验证优雅停机时，向进程发
`SIGINT/SIGTERM`，不要在业务代码重复注册信号。

失败时最多修复 3 轮。每轮记录失败命令、根因、修改和重跑结果；同一根因连续失败、缺凭据/外部服务、
需要扩大授权或第 3 轮仍失败时停止并明确报告，不伪造通过。

交付必须包含：精确 tgf 版本、生成参数、通过的命令、未运行的外部集成、运行方式和下一步扩展建议。

## 8. 框架问题升级

先用最小业务项目确认不是配置/API 误用。确认框架缺陷后收集 tgf/Go 版本、形态、最小复现、期望/实际、
脱敏日志；删除 token、密码、AEAD key、内网地址和真实玩家数据。只有用户授权且 `gh auth status` 可用时
才创建 issue，否则输出可复制的 issue 正文。

## 9. Skill 维护资源

- [生成器契约](references/generator-contract.md)
- [项目规范](references/project-standards.md)
- [场景指南](references/scenario-guide.md)
- [API 速查](references/api-reference.md)
- [Skill 静态契约检查](scripts/check_skill_contract.py)
- [生成器外部 consumer 前向测试](scripts/forward_test_generator.py)
- [Codex 界面与调用策略元数据](agents/openai.yaml)

修改本 Skill 后必须同步仓库源与安装副本，并依次运行 `skill-creator/quick_validate.py`、
`t-sys-skill/lint_skill.py`、静态契约检查和外部 consumer 前向测试。
