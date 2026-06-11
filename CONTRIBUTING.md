# 贡献指南（CONTRIBUTING）

感谢为 **tgf**（`github.com/thkhxm/tgf/v2`）贡献代码 / 文档 / issue！本文档说明
PR 流程、提交规范、测试要求与编码规范。提 bug / 功能建议的完整流程见
[`doc/issue-workflow.md`](doc/issue-workflow.md)。

---

## 1. 开发环境

- **Go 工具链 ≥ 1.26**（`go.mod` 声明 `go 1.26.0` / `toolchain go1.26.4`）。
- tgf 与两个 fork（`rpcx` / `rpcx-consul`）一起放在一个 Go **workspace**。贡献者从源码
  构建时，按 [`doc/architecture.md`](doc/architecture.md) 的布局把三个仓库检出到同级目录：

  ```
  <workspace>/tgf          ← 本仓库
  <workspace>/rpcx         ← thkhxm/rpcx fork
  <workspace>/rpcx-consul  ← thkhxm/rpcx-consul fork
  ```

  `tgf/go.mod` 内置指向 `../rpcx`、`../rpcx-consul` 的 path replace，因此 workspace 内与
  `GOWORK=off` 单仓模式均可直接构建——**本地改 fork 立即生效，无需 `go get` 或 bump 版本**。
- 中国大陆环境建议预先导出代理（见 `Makefile` 头部注释）：

  ```bash
  export GOPROXY=https://goproxy.cn,direct
  export GOSUMDB=sum.golang.google.cn
  ```

- **真实凭据绝不入库**：`.env.dev` / `.env.test` / `.env.release` 已被 `.gitignore`
  忽略，只提交占位模板（`.env.example` 等）。

---

## 2. PR 流程

1. **Fork** 本仓库到你的账号。
2. 从默认分支拉 **feature 分支**，命名 `feature/xxx`（新功能）/ `fix/xxx`（修复）/
   `docs/xxx`（文档）。修 issue 时推荐直接 `gh issue develop <n> --name fix/<n>-xxx --checkout`
   建关联分支（见 issue-workflow.md §2.4）。
3. 写代码 **+ 配套单元测试 + 必要的 `example/` 示例 / 文档**（见 §4 测试要求；新增公开
   API 没测试 / 没示例的 PR 不予合并）。
4. 本地跑通自检（缺一不可）：

   ```bash
   make fmt          # gofmt -w .
   make vet          # go vet ./...
   make lint         # golangci-lint run ./...（配置见 .golangci.yml）
   make test         # go test -race -count=1 ./...
   ```

5. 提交（commit message 遵循 §3 规范），push 到你的 fork。
6. 开 PR 到 `thkhxm/tgf` 默认分支，PR 描述说明**动机 / 改动点 / 影响面 / 测试方式**；
   修复 issue 的在描述里写 `fixes #编号`（合并到默认分支后自动关闭 issue）。
7. 通过 CI（`.github/workflows/ci.yml`：unit + integration + golangci-lint 三个 job）
   与 review 后合并。

---

## 3. 提交规范（commit message）

沿用仓库现状：**Conventional Commits 类型前缀 + 中文描述**。

**格式**：

```
<type>: <中文简述（一句话，祈使语气）>

<可选的中文正文：解释为什么这么改、影响面、关联 issue>

<可选 footer：fixes #123 等关联词>
```

**常用 type**（取自仓库历史）：

| type | 用途 |
|------|------|
| `feat` | 新功能 / 新 API |
| `fix` | 缺陷修复 |
| `docs` | 文档 |
| `chore` | 杂项（不影响功能的维护） |
| `build` | 构建 / 模块路径 / 依赖布局 |
| `deps` | 依赖版本升级 |
| `refactor` | 重构（不改外部行为） |
| `test` | 仅测试改动 |

**真实示例**（来自本仓库 git log）：

```
feat: G 档 HTTP 一等公民 - web 服务 + HTTP-RPC 桥 + client-only + Consul 注册
fix(rpc): 修复单进程二次登录 panic

二次登录时 loginCoord 未释放旧连接导致 nil 解引用，本次在 handleConn 退出前补释放。

fixes #123
chore: 升级 Go 到 1.26（go 1.26.0 / toolchain go1.26.4）
```

> `<type>` 后可选带 scope（如 `fix(rpc):` / `feat(web):`），与仓库现状一致即可。
> 描述统一用**中文**；代码标识符（函数名 / API / 包名）保留英文原样。

---

## 4. 测试要求

### 4.1 单元测试（每个 PR 必跑）

```bash
make test
# 等价于：go test -race -count=1 -timeout=5m ./...
```

- **必须带 `-race`**：框架重度使用 goroutine / 缓存 / 连接管理，竞态检测是底线。
- 测试与源码同目录，命名 `*_test.go`；最小 server 引导样板见
  `rpc/rpcserver_example_test.go`。
- 新增 / 修改公开 API **必须**补对应单测；修 bug 优先补一个**能复现该 bug 的回归测试**。

### 4.2 集成测试（涉及 Redis / MySQL / Consul 的改动必跑）

集成测试以 `//go:build integration` tag 隔离，默认不编译；需显式 `-tags integration`：

```bash
go test -tags integration -count=1 -timeout=15m ./...
```

- 需本机起 Redis / MySQL / Consul（连接参数走 `.env.test` 或环境变量，见 CI
  `integration` job 的 env 配置）。
- 改动**网关连接 / write-behind 落库 / 服务发现 / 跨节点登录**等路径时，单元测试外
  必须补跑集成测试。

### 4.3 CI

PR 会触发 [`.github/workflows/ci.yml`](.github/workflows/ci.yml) 三个 job：

| job | 内容 |
|-----|------|
| `unit` | `go vet` + `go test -race`（默认 build tag，集成测试自动排除） |
| `integration` | 起 redis/mysql/consul 容器后 `go test -tags integration` |
| `lint` | `golangci-lint`（版本与 `.golangci.yml` 对齐） |

三个 job 全绿是合并前提。

---

## 5. 编码规范

- 详细规范见 [`doc/coding-style.md`](doc/coding-style.md)（命名 / 错误处理 / 泛型约束 /
  builder 模式 / 日志 tag 等约定）。
- 提交前过 `make fmt`（`gofmt -w .`）与 `make lint`（`golangci-lint run`，配置
  `.golangci.yml`）。
- 框架既有约定速记：
  - 大量使用 **fluent builder**（`Server` / `tcpBuilder` / `AutoCacheBuilder`），扩展时
    沿用 `With*` 链式风格。
  - `db/`、`util/` 大量使用**泛型**，扩展时沿用既有约束并集。
  - 多数文件以中文头注释块（`@Link` / `@Description` / 日期）起头——新增同包文件请保持风格。
  - 日志优先用 tagged helper（`log.InfoTag("init", ...)` / 热路径用 `log.InfoTagW(...)`
    的 `zap.Field` 风格）；trace id 通过 context 透传。
  - RPC handler 从 context 用 `tgf/define.go` 里的 `ContextKeyXxx` 常量读调用方身份，
    **不要自创 context key**。

---

## 6. 报告问题

- **bug / 功能建议**：见 [`doc/issue-workflow.md`](doc/issue-workflow.md)（含一键 `gh` 提交
  模板与脱敏规则）。提交前请用对应表单（空白 issue 已禁用）。
- **安全问题**：涉及凭据泄露 / 远程利用等敏感问题，**不要**公开提 issue，请邮件联系
  维护者（`thkhxm@gmail.com`）私下沟通。

---

感谢你的贡献 🎮
