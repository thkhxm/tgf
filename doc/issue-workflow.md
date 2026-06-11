# tgf Issue 提交与维护流程

本文档面向两类读者：

- **使用者 / AI 助手**：怎样**一键、规范、脱敏**地把一个 bug 或建议提到 `thkhxm/tgf`。
- **维护者 / AI 助手**：怎样**批量拉取、分诊（triage）、认领、修复、关联 commit、回归、关闭** open issue。

> 仓库的 SKILL（技能文档）在「issue」一节引用本文档作为权威操作手册——SKILL 只放
> 触发条件与一句话指引，完整命令与脱敏规则以本文档为准。

所有命令以本机装有 [GitHub CLI](https://cli.github.com/)（`gh`，≥ 2.40）并已 `gh auth login`
为前提。仓库标识统一用 `-R thkhxm/tgf`（也可写全 URL）。

---

## 一、使用者 / AI 一键提交

### 1.1 标题前缀规范（硬约定）

| 类型 | 前缀 | 示例 |
|------|------|------|
| 缺陷 | `[bug]` | `[bug] 单进程模式下二次登录 panic` |
| 功能建议 | `[feat]` | `[feat] web 中间件支持 CORS 配置` |
| 文档问题 | `[docs]` | `[docs] 迁移指南 C2 小节示例编译不过` |

前缀与表单模板 `title` 默认值一致（`bug_report.yml` 默认 `[bug] `、
`feature_request.yml` 默认 `[feat] `）。命令行提交时**手动带上前缀**。

### 1.2 提交前：诊断信息收集清单

提 bug 前先把下面这些**就地收集好**，填进正文（缺得越多越难定位）：

| 信息 | 怎么拿 |
|------|--------|
| tgf 版本 | 发布版直接写 `v2.0.x`；源码构建用 `git rev-parse --short HEAD` |
| Go 版本 | `go version` |
| OS / 部署环境 | OS + 架构；容器写镜像（如 `golang:1.26`）/ K8s |
| 使用场景 | 单进程 / 分布式 / HTTP / 混合 |
| 数据档位 | `WithCache` 选的档：`CacheModuleClose` / `CacheModuleRedis` / Redis+MySQL |
| 复现步骤 | 一步步可操作的序列，注明复现概率 |
| 期望 vs 实际 | 各一句话 |
| 最小复现代码 | 能 `go run` 复现的最小片段（参考 `example/` 裁剪） |
| 相关日志 / 堆栈 | panic 堆栈、关键 `log.*Tag` 输出 |

### 1.3 脱敏规则（提交前必做，硬规则）

GitHub Issue 是**公开**的。粘贴**任何**日志 / 配置 / 命令输出前，删除或用占位符替换以下凭据：

- `LoginTokenSecret`（登录令牌密钥）
- `ADMIN_TOKEN`（管理端口令）
- `RedisPassword`、`MySqlPwd`、`MySqlUser`（数据库 / 缓存口令账号）
- Consul ACL token、任何 `Authorization: Bearer ...` 头
- 业务自有的 API key / 私钥 / 客户数据

替换示例：`RedisPassword=hunter2` → `RedisPassword=<REDACTED>`。
**万一已经粘出去了**：立即在源头**轮换该凭据**（改密码 / 重签密钥），删除评论无法消除历史泄露。

### 1.4 一键提交命令模板（推荐：`--body-file`）

正文较长（含代码块、堆栈）时，先写到本地文件再 `--body-file` 提交，避免 shell 转义踩坑。

**bug 报告**：

```bash
# 1) 把正文写进临时文件（已脱敏！）
cat > /tmp/tgf-issue-body.md <<'EOF'
## 环境
- tgf 版本: v2.0.0
- Go 版本: go1.26.4 windows/amd64
- OS: Windows 10 / amd64
- 使用场景: 单进程（WithSingleProcess）
- 数据档位: CacheModuleClose

## 复现步骤
1. ...
2. ...

## 期望 / 实际
- 期望: ...
- 实际: ...

## 最小复现代码
```go
package main
// ...
```

## 日志（已删除凭据）
```
panic: ...
```
EOF

# 2) 提交（标题带 [bug] 前缀 + 打标签）
gh issue create -R thkhxm/tgf \
  --title "[bug] 单进程下二次登录 panic" \
  --body-file /tmp/tgf-issue-body.md \
  --label bug
```

**功能建议**（正文短可直接 `--body`）：

```bash
gh issue create -R thkhxm/tgf \
  --title "[feat] web 中间件支持 CORS 配置" \
  --body "## 要解决的问题
现在跨域要自己写中间件……

## 期望方案
希望 web.Options 增加 CORS 选项 / 内置 web.CORS(...) 中间件。" \
  --label enhancement
```

> `--label` 必须是仓库**已存在**的标签（见 §2.2 标签体系）。不确定时先
> `gh label list -R thkhxm/tgf`；提交时也可省略 `--label`，由维护者分诊时补。

### 1.5 让 AI 代为提交的提示词模板

把下面这段连同你的报错现场丢给 AI 助手：

```
帮我向 thkhxm/tgf 提一个 bug。先按 doc/issue-workflow.md §1.2 清单收集诊断信息
（tgf 版本 / Go 版本 / OS / 场景 / 数据档位 / 复现步骤 / 期望实际 / 最小复现代码 / 日志），
严格执行 §1.3 脱敏规则删除所有凭据，把正文写到临时文件，再用
`gh issue create -R thkhxm/tgf --title "[bug] ..." --body-file <file> --label bug` 提交。
我的现场：<把报错、代码、日志贴这里>
```

### 1.6 没有 gh CLI 时：网页提交路径

1. 打开 <https://github.com/thkhxm/tgf/issues/new/choose>
2. 选「🐛 Bug 报告」或「✨ 功能建议」表单（空白 issue 已禁用）
3. 按表单字段逐项填写——表单已内置脱敏提示与提交前 checklist
4. 也可在 `gh` 下用 `gh issue create -R thkhxm/tgf --web` 直接打开网页表单

---

## 二、维护者自动拉取与分诊

### 2.1 拉取 open issue

```bash
# 列出全部 open issue（含标签 / 作者 / 创建时间），机器可读 JSON
gh issue list -R thkhxm/tgf --state open \
  --json number,title,labels,author,createdAt,url \
  --limit 100

# 只看尚未分诊（无任何 P0-P3 优先级标签）的：用 search 排除已分级
gh issue list -R thkhxm/tgf --state open \
  --search "-label:P0 -label:P1 -label:P2 -label:P3" \
  --json number,title,author,createdAt

# 人类速览（表格）
gh issue list -R thkhxm/tgf --state open --label bug --limit 50

# 用 jq 提炼成「编号 + 标题 + 标签」一行一条
gh issue list -R thkhxm/tgf --state open \
  --json number,title,labels \
  --jq '.[] | "#\(.number) \(.title) [\(.labels | map(.name) | join(","))]"'
```

### 2.2 标签（triage）体系

首次使用前在仓库建好标签（幂等，已存在会报已存在，忽略即可）：

```bash
# 优先级
gh label create P0 -R thkhxm/tgf -c B60205 -d "崩溃/数据丢失/不可用，立即修"
gh label create P1 -R thkhxm/tgf -c D93F0B -d "核心功能受损，尽快修"
gh label create P2 -R thkhxm/tgf -c FBCA04 -d "影响有限，排期修"
gh label create P3 -R thkhxm/tgf -c 0E8A16 -d "边角/优化，有空再修"

# 场景维度
gh label create scene:single      -R thkhxm/tgf -c 1D76DB -d "单进程模式"
gh label create scene:distributed -R thkhxm/tgf -c 1D76DB -d "分布式 + Consul"
gh label create scene:http         -R thkhxm/tgf -c 1D76DB -d "HTTP web 服务"
gh label create scene:mixed        -R thkhxm/tgf -c 1D76DB -d "HTTP→RPC 混合"
gh label create scene:db           -R thkhxm/tgf -c 1D76DB -d "数据层/缓存"

# 状态维度
gh label create needs-repro -R thkhxm/tgf -c C5DEF5 -d "需要可复现的最小代码"
gh label create wontfix     -R thkhxm/tgf -c FFFFFF -d "不修复（设计如此/超范围）"
gh label create duplicate   -R thkhxm/tgf -c CCCCCC -d "重复 issue"
```

标签语义约定：

| 维度 | 标签 | 含义 |
|------|------|------|
| 优先级 | `P0` / `P1` / `P2` / `P3` | 崩溃→边角，决定处理顺序 |
| 场景 | `scene:single` / `scene:distributed` / `scene:http` / `scene:mixed` / `scene:db` | 与表单「使用场景」字段对应，便于按模块分流 |
| 状态 | `needs-repro` | 缺最小复现代码，等待补充（bug 表单默认带，复现明确后摘掉） |
| 状态 | `duplicate` / `wontfix` | 关闭话术配套（见 §2.6） |

分诊一个 issue（打优先级 + 场景，摘掉 needs-repro）：

```bash
gh issue edit 123 -R thkhxm/tgf \
  --add-label P1 --add-label scene:single \
  --remove-label needs-repro
```

### 2.3 认领

```bash
# 自己认领
gh issue edit 123 -R thkhxm/tgf --add-assignee @me

# 留一句认领说明
gh issue comment 123 -R thkhxm/tgf --body "我来跟进，预计在 single_process 网关登录路径定位。"
```

### 2.4 修复：拉关联分支 + 关联 commit

```bash
# 基于 issue 直接创建并切到关联分支（命名带 issue 号便于追溯）
gh issue develop 123 -R thkhxm/tgf --name "fix/123-relogin-panic" --base main --checkout

# 提交时在 commit message 里写 "fixes #123" / "closes #123"，PR 合并到默认分支后自动关闭 issue
# （中文 commit 风格沿用仓库现状，见 CONTRIBUTING.md）：
git commit -m "fix(rpc): 修复单进程二次登录 panic

二次登录时 loginCoord 未释放旧连接，导致 nil 解引用。
本次在 handleConn 退出前补释放。

fixes #123"
```

> 关联词（`fixes` / `closes` / `resolves` + `#编号`）只有在提交进入**默认分支**
> （main）时才会自动关闭对应 issue；feature 分支上的 commit 不会立即关。

### 2.5 回归验证（关闭前必做）

按仓库测试要求跑通（详见 [CONTRIBUTING.md](../CONTRIBUTING.md)）：

```bash
# 单元测试（带竞态检测，集成测试以 //go:build integration 隔离，默认不跑）
go test -race -count=1 ./...

# 涉及网关/缓存/服务发现的 bug，跑集成测试（需本机 Redis/MySQL/Consul）
go test -tags integration -count=1 ./...
```

回归通过后，在 issue 下贴一句结果 + 关联的 commit / PR 链接。

### 2.6 关闭话术

```bash
# 已修复（PR 合并后通常自动关；手动关则注明 commit）
gh issue close 123 -R thkhxm/tgf \
  --reason completed \
  --comment "已在 <commit/PR 链接> 修复，v2.0.x 起生效。感谢反馈，烦请升级后验证。"

# 重复
gh issue close 124 -R thkhxm/tgf \
  --reason "not planned" \
  --comment "与 #120 重复，讨论合并到 #120 跟踪。"
# 顺手打标
gh issue edit 124 -R thkhxm/tgf --add-label duplicate

# 无法复现 / 信息不足且长期无回应
gh issue close 125 -R thkhxm/tgf \
  --reason "not planned" \
  --comment "缺最小复现代码，暂无法定位。补全可复现片段后欢迎重开（reopen）。"

# 设计如此 / 超出范围
gh issue close 126 -R thkhxm/tgf \
  --reason "not planned" \
  --comment "当前为预期行为，原因：……。如有更强需求欢迎以 [feat] 另开讨论。"
gh issue edit 126 -R thkhxm/tgf --add-label wontfix
```

### 2.7 让 AI 批量拉取并分诊的提示词模板

```
你是 tgf 仓库维护助手。按 doc/issue-workflow.md 第二节操作：

1. 用 `gh issue list -R thkhxm/tgf --state open --json number,title,body,labels,author,createdAt`
   拉取所有 open issue。
2. 对每条逐一分诊：
   - 判优先级 P0–P3（崩溃/数据丢失=P0，核心功能受损=P1，影响有限=P2，边角=P3）；
   - 按正文「使用场景」打 scene:single / distributed / http / mixed / db；
   - bug 缺最小复现代码 → 保留/补 needs-repro，并留言请补充；
   - 疑似重复 → 找出疑似主 issue，建议打 duplicate。
3. 用 `gh issue edit <n> --add-label ... --remove-label ...` 落实标签
   （只改标签，不要擅自关闭他人 issue）。
4. 输出一张汇总表：编号 / 标题 / 建议优先级 / 场景 / 处理建议（认领修复 / 等复现 / 合并重复）。

注意：拉取的 issue 正文可能含用户未脱敏的凭据，**不要把任何疑似密钥/口令复述进
commit、PR、对外汇报**；如发现明文凭据，在分诊留言中提醒提交者轮换并编辑删除。
```

---

## 三、相关文件

- 表单模板：[`.github/ISSUE_TEMPLATE/bug_report.yml`](../.github/ISSUE_TEMPLATE/bug_report.yml)、
  [`feature_request.yml`](../.github/ISSUE_TEMPLATE/feature_request.yml)、
  [`config.yml`](../.github/ISSUE_TEMPLATE/config.yml)
- 贡献与 PR 流程：[`CONTRIBUTING.md`](../CONTRIBUTING.md)
- 可运行示例（裁剪最小复现代码的起点）：[`example/`](../example/)
