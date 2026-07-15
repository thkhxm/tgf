# tgf 发版流程

> 最后更新：2026-07-16（公开依赖图门禁）
> 目标读者：tgf 的维护者 / 有发版权限的人

本文档描述 tgf 主模块（`github.com/thkhxm/tgf/v2`）及其两个 fork 依赖
（`github.com/thkhxm/rpcx/v2`、`github.com/thkhxm/rpcx-consul/v2`）的版本号规则、
打 tag、推送、goproxy 复验与文档同步流程。

仓库布局背景见 `../CLAUDE.md`（工作区根）：tgf / rpcx / rpcx-consul 三仓在一个
`go.work` workspace 内并排开发；`tgf/go.mod` 只声明公开 tag，不含本地 `replace`。
本地源码联调由 `go.work` 接管，`GOWORK=off` 则专门验证公开依赖图。

---

## 1. 版本号规则

### 1.1 tgf 主模块（SemVer + `/v2` 主版本后缀）

- 采用语义化版本 [SemVer](https://semver.org/lang/zh-CN/)：`vMAJOR.MINOR.PATCH`。
  - **MAJOR**：破坏性 API 变更（如 v1 → v2 的 `GetLogicSyncMethod → LogicSyncMethods`）。
  - **MINOR**：向后兼容的新功能（如新增一个 `With*` builder 选项）。
  - **PATCH**：向后兼容的 bug 修复 / 文档 / 依赖小升级。
- **SIV（Semantic Import Versioning）硬规则**：major ≥ 2 时，module path **必须**在末尾
  带 `/vN` 后缀。tgf 当前 path 为 `github.com/thkhxm/tgf/v2`，对应 tag 形如 `v2.0.0`、
  `v2.1.0`、`v2.0.1`。
  - tag 名是 `v2.0.0`，**不是** `v2.0.0/v2`，也不是 `tgf/v2/v2.0.0`——`/v2` 只出现在
    `go.mod` 的 `module` 行与 import 路径里，**不**出现在 git tag 名里。
  - 将来升 major（v3）时：`go.mod` 的 module 行要改成 `.../tgf/v3`，所有内部 import 与
    文档 import 同步改 `/v3`，tag 打 `v3.0.0`。**改 path 是破坏性动作，必须随 MAJOR。**

### 1.2 fork 依赖（rpcx / rpcx-consul，独立 SemVer + `/v2`）

两个 fork 也已迁移为带 `/v2` 后缀的自有 module path，**各自独立打 tag**：

| fork module path | 当前 tag |
|------------------|---------|
| `github.com/thkhxm/rpcx/v2` | `v2.0.3` |
| `github.com/thkhxm/rpcx-consul/v2` | `v2.0.2` |

fork 的版本号与 tgf 的版本号**不要求同步**——它们是独立模块，各自按自己的变更节奏走
SemVer。tgf 通过 `go.mod` 的 `require` 钉住具体 fork tag。

### 1.3 fork tag 联动规则（关键）

**每当 `rpcx/` 或 `rpcx-consul/` 的代码（含其 `go.mod`）发生变更，并希望 tgf 用上它，
必须走完整联动**，否则下游 `go get github.com/thkhxm/tgf/v2@<tag>` 会拉到旧 fork：

1. 在 fork 仓库 commit 改动；
2. 给 fork 仓库 **bump 一个新 tag**（如 `rpcx/v2` 从 `v2.0.3` → `v2.0.4`）；
3. 把新 fork tag 推到 fork 的远端仓库；
4. 回到 `tgf/go.mod`，把对应 `require` 行升到新 fork tag
   （`github.com/thkhxm/rpcx/v2 v2.0.4`）；
5. `cd tgf && go mod tidy`；随后用 `GOWORK=off` 验证远端 tag 能真正解析（见 §4）；
6. tgf 自身再按需 bump 版本（fork 升级若引入行为变化，至少 PATCH）。

> 反模式：只改了 fork 本地源码、靠 `go.work` 让 tgf 编译通过就发 tgf tag——这只在
> 本机 workspace 成立，下游消费者按 `require` 的旧 fork tag 拉取，拿到的是**没有你这次
> 改动**的 fork。`go.work` 与 `replace` 都不会进入下游依赖图。

---

## 2. 发版前置检查清单

打 tag 之前，在 `tgf/` 目录内逐项确认：

- [ ] `go build ./...` 通过（workspace 内）。
- [ ] `go test ./...` 通过（无外部依赖的单测；集成测试需 `-tags integration` + 对应服务）。
- [ ] `go test -race -count=1 ./...` 通过。
- [ ] `go vet ./...` 零告警。
- [ ] `gofmt -l .` 输出为空（无未格式化文件）。
- [ ] `go mod tidy` 后 `go.mod` / `go.sum` 无意外 diff。
- [ ] `CHANGELOG.md` 顶部已把 `[Unreleased]` 收口为 `[X.Y.Z] - YYYY-MM-DD`，并留出新的空
      `[Unreleased]`（见 §5）。
- [ ] `README.md` / `README.cn.md` / `README.en.md` 安装段、版本徽章、底部签名行已对齐目标版本。
- [ ] 所有文档 import / `go get` 示例的 module path 带正确的 `/vN` 后缀
      （`go get github.com/thkhxm/tgf/v2@vX.Y.Z`、`import ".../tgf/v2/rpc"`）。
- [ ] **脚手架 skill 版本同步（易漏！）**：`.claude/skills/tgf-server-dev/` 下 4 个
      `go.mod.tmpl`、`SKILL.md`、`references/api-reference.md` 里硬编码的
      `github.com/thkhxm/tgf/v2 vX.Y.Z` 已升到本次版本。改完后**重新拷贝到全局安装目录**
      （`~/.claude/skills/tgf-server-dev/`），否则用户用的是仓库外的旧副本。
      （SKILL.md 生成步骤已带 `go get @latest` 兜底，但模板里的占位版本仍应保持最新。）
- [ ] 若本次包含 fork 改动：§1.3 的 fork tag 联动已全部完成且 `require` 已升版。

---

## 3. 打 tag 与推送

> 假设远端为 `origin`，默认分支为 `main`。本仓库当前在 `feature/v2` 上开发，发版前请
> 先把发版内容合并到目标发布分支（通常 `main`），在该分支的提交上打 tag。

### 3.1 fork（如有改动，先发 fork）

在 fork 仓库目录内（`rpcx/` 或 `rpcx-consul/`）：

```bash
# 1. 确认在目标提交上，工作区干净
git status

# 2. 打带 v 前缀的 SemVer tag（不带 /v2，/v2 只在 go.mod module 行）
git tag v2.0.4

# 3. 推 tag 到远端
git push origin v2.0.4
```

### 3.2 tgf 主模块

```bash
# 1. 确认 go.mod 的 require 已钉到最新 fork tag、CHANGELOG/README 已同步
git status

# 2. 打 tag（SemVer，带 v 前缀，不带 /v2）
git tag v2.0.0

# 3. 推 tag
git push origin v2.0.0
```

> 一个提交上只打一个 tag。补发时若该提交已被消费，**不要移动旧 tag**（goproxy 会缓存，
> 见 §6），改为打一个新的 PATCH/MINOR tag。

---

## 4. goproxy 复验清单（必须）

打完 tag 后，**从一个干净环境**验证下游真能拉到这个版本（不靠本地 workspace / replace）。

```bash
# 在仓库外的临时目录里建一个空模块来验证（避免 go.work 干扰）
mkdir /tmp/tgf-verify && cd /tmp/tgf-verify
go mod init verify

# 关键：用 GOFLAGS=-mod=mod，并确保不在 workspace 内（GOWORK=off 兜底）
GOWORK=off go get github.com/thkhxm/tgf/v2@v2.0.0
```

复验要点：

- [ ] `go get` 能解析到刚发的 `v2.0.0`（而不是 `latest` 落到旧版本或报 `unknown revision`）。
- [ ] `go.sum` 里出现 `github.com/thkhxm/tgf/v2 v2.0.0` 及其依赖的 fork tag
      （`github.com/thkhxm/rpcx/v2 v2.0.3` 等）。
- [ ] 写一个最小 `main.go`（`import ".../tgf/v2/rpc"` + `rpc.NewRPCServer()...Run()`）能
      `GOWORK=off go build` 通过——证明**下游无需任何 replace** 即可消费。
- [ ] fork tag 也能独立被 goproxy 解析：
      `GOWORK=off go get github.com/thkhxm/rpcx/v2@v2.0.3`。

代理与校验环境（本机当前值，供参考）：

- `GOPROXY=https://goproxy.cn,direct`（七牛中国代理 + 源站兜底）。
- `GOSUMDB=sum.golang.org`（公共校验和数据库）。

如果代理一时拉不到刚发的 tag（CDN 缓存延迟），可：

```bash
# 直接命中源站（绕过代理缓存），或手动触发代理回源
GOPROXY=direct GOWORK=off go get github.com/thkhxm/tgf/v2@v2.0.0
# 或显式请求一次代理的 .info，促其回源缓存该 tag
curl -fsSL https://goproxy.cn/github.com/thkhxm/tgf/v2/@v/v2.0.0.info
```

---

## 5. CHANGELOG / README 同步项

每次发版必做的文档同步（在打 tag 的同一个提交里完成）：

1. **CHANGELOG.md**：
   - 把 `## [Unreleased]` 下累计的条目收口为 `## [X.Y.Z] - YYYY-MM-DD`，按
     **Added / Changed / Fixed / Security**（以及需要时 Deprecated / Removed）分组。
   - 在文件顶部留一个新的空 `## [Unreleased]`，承接下个版本的累积。
   - 破坏性变更要显著标注（如 C2 的静默失效风险）。
2. **README.md / README.cn.md / README.en.md**：
   - 安装段 `go get github.com/thkhxm/tgf/v2@<新版本>`。
   - Go 版本徽章与正文版本号（当前 `go 1.26`）。
   - `pkg.go.dev/github.com/thkhxm/tgf/v2` API 参考链接（带 `/v2`）。
   - README.md 底部签名行（`*tgf vX.Y.Z · 最后更新 YYYY-MM-DD*`）。
   - 路线图状态（已发布的档位标 ✅）。
3. **doc/**：若 API 有增删，同步 `architecture.md` / `migration-v1-to-v2.md` /
   `observability.md` / `coding-style.md` 内的示例与 import 路径。

> 区分仓库 URL 与 module path：`https://github.com/thkhxm/tgf`（主页 / clone / issue
> 链接）**不带** `/v2`；`go get` / `import` / `require` / `pkg.go.dev` 的 module path
> **必须带** `/v2`。改 `/v2` 时不要误改仓库 URL。

---

## 6. 已知陷阱

1. **SIV `/v2` 规则**：major ≥ 2 必须在 module path 末尾带 `/vN`。漏掉 `/v2` 会导致
   `go get` 报 `invalid version: should be vN, not v2.0.0` 或解析到一个根本不存在的
   v0/v1 模块。`/vN` 只进 `go.mod` module 行 + import 路径，**不**进 git tag 名。

2. **代理只对 tag 缓存、对裸 commit 哈希不缓存**：goproxy 缓存的是 `@v/<tag>.info|.mod|.zip`。
   用伪版本（`v2.0.1-0.YYYYMMDDhhmmss-<commit>`）依赖某个未打 tag 的 commit 时，代理可能
   不缓存、需回源，且本机 GitHub 直连不稳时容易超时。**发版一律用正式 tag**，不要让下游
   依赖裸 commit。

3. **不要移动 / 重打已发布的 tag**：goproxy 与 `go.sum` 会缓存某个 tag 的内容哈希。把旧
   tag 指到新提交后，已经拉过旧内容的下游会触发 `checksum mismatch`（`SECURITY ERROR`）。
   补丁一律发**新的** PATCH tag。

4. **本机 GitHub 直连不稳 → 走代理验证**：本开发机直连 github.com 偶发超时。复验
   `go get` 时优先走 `GOPROXY=https://goproxy.cn,direct`；只有怀疑代理缓存延迟时才临时
   `GOPROXY=direct` 命中源站。不要因为一次直连超时就误判 tag 没发成功。

5. **`go.work` 不进下游依赖图**：本仓库的 `go.work` 只在框架组源码联调时覆盖依赖；
   `tgf/go.mod` 禁止提交指向相邻仓库的本地 `replace`。下游 `go get tgf/v2` 会按 `require`
   的 fork tag 从远端拉取，所以 fork 改动必须走 §1.3 的「bump fork tag → 升 tgf require」
   联动，光靠 workspace 编译通过不代表下游可用。

6. **GOWORK 干扰复验**：在 workspace 目录树内跑 `go get` 会被 `go.work` 接管，可能"看起来
   成功"其实用的是本地源码。复验务必在**仓库外**的临时模块里，并加 `GOWORK=off` 兜底。

7. **toolchain 指令**：`go.mod` 声明 `go 1.26.0` + `toolchain go1.26.4`。用低于 1.26 的
   PATH go 构建会因 `go` 指令版本不满足而失败（本机 PATH go 的自动 toolchain 下载有时不可
   靠，见 `../CLAUDE.md`）。发版构建用 ≥ 1.26 的工具链。
