# tgfctl 生成器契约

## 命令

```text
tgfctl init --module <path> --dir <destination>
  --profile single|distributed|http-rest|http-rpc
  --data none|redis|redis-mysql
  --protocol none|tcp|websocket|kcp|all
  --deploy local|compose|k8s
  --security dev-generated|external
  --tgf-version latest|v2.x.y|<commit>
  [--force] [--git-init] [--skip-checks] [--json]

tgfctl verify --dir <generated-project> [--json]
tgfctl version
```

`init` 默认解析版本、生成、`go mod tidy`、`go build ./...`、`go vet ./...`、来源验证；
`--skip-checks` 只用于生成器自身的离线/渲染测试，Skill 正常交付不得使用。

## 参数交叉约束

- `single`: `data=none|redis|redis-mysql`，`protocol=tcp|websocket|kcp|all`。
- `distributed`: `data=redis|redis-mysql`，`protocol=tcp|websocket|kcp|all`。
- `http-rest` / `http-rpc`: `protocol=none`；数据层三档均可。
- `deploy=compose` 输出 Dockerfile 与 Compose；`deploy=k8s` 输出 Dockerfile 与 K8s。
- `security=dev-generated` 用 `crypto/rand` 生成本地开发凭据；`external` 不生成真实 secret。

非法组合必须在写目标目录前失败，返回非零码且不留下半成品。

## 文件与秘密

- 模板仅存在于 CLI 的 embedded FS，按路径排序渲染，输出固定 LF 和固定普通文件 mode。
- 输出 `.env.example`；`dev-generated` 额外输出已被 `.gitignore` 覆盖的 `.env.dev`。
- login/admin token 至少取 32 字节随机数并用 base64url/hex 表示；KCP key 恰好 32 字节。
- stdout、JSON、README、manifest、错误和 Git 可跟踪文件中不得出现 secret。
- 默认目标必须不存在；`--force` 最多接受空目录。先在同级临时目录完成全部检查，再 rename。
- 默认不初始化 Git；`--git-init` 才执行。

## 版本与来源

`latest`、tag、commit 都必须先经 Go module resolver 解析为精确版本，然后写入 `go.mod`。
生成项目直接 require `github.com/thkhxm/tgf/v2`，不得添加任何本地 replace。

`verify` 在项目目录正常继承 Go workspace 行为，不设置 `GOWORK=off`，并要求：

- go.mod 存在 direct tgf require，版本固定；
- `go list -m -json github.com/thkhxm/tgf/v2` 的 `Version` 非空；
- `Replace == nil` 且 `Main == false`；
- active go.work 即使属于业务项目，也不能把本地 tgf checkout 作为 workspace module/use/replace。

发布隔离可以额外执行 `GOWORK=off`，但不能把它变成业务项目永久政策。

## 原子性与退出

- 目标冲突、模板渲染、版本解析、Go 检查或来源验证任一步失败，都清理 CLI 创建的临时目录。
- 不删除、不覆盖、不合并非空用户目录。
- `--json` 只输出非敏感参数、精确版本、目标路径和检查结果，便于自动化消费。
