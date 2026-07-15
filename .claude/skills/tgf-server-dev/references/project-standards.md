# tgf v2 业务项目统一规范

## 布局与职责

单进程原型可以平铺；出现两个以上 module 或进程后必须采用：

```text
cmd/<process>/main.go
internal/api/
internal/service/
internal/dao/
configs/
deploy/
```

`main` 只装配 builder 并等待 `Run()` 返回。框架拥有进程信号；业务代码不要再次 `signal.Notify`。

## RPC 与服务生命周期

- `Startup()` 只有 `(true, nil)` 表示可服务；其他结果不得进入发现或本地 dispatcher。
- 失败或未启用的服务仍可能收到 `Destroy`，清理必须可重复且容忍部分初始化。
- 契约集中在 `internal/api`；服务间调用使用 `ServiceAPI.NewRPC`，request/reply 使用指针。
- TCP/WS/KCP 客户端帧直达必须使用 protobuf 和 `rpc.Args[T]` / `rpc.Reply[T]`。
- 不混用统一 `WithGatewayOptions` 与 deprecated 网关 builder；网关端口与内部 RPC 端口错开。

## 配置与安全

- 框架配置从 `config.Current()` 读取；业务自有 env 也应集中封装。
- `LoginTokenSecret` 不配时登录 fail-closed；生产严禁 `WithoutLoginCheck()`。
- admin token 不配时控制面 fail-closed；生产 secret 走部署环境/K8s Secret。
- KCP 使用 32 字节 AEAD key；明文 KCP 只限明确的内网实验。
- `.gitignore` 必须忽略 `.env.*` 并放行 `.env.example`；禁止把 secret 写入 README、镜像层和日志。

## 错误与可观测性

- RPC、DB、HTTP bridge 的 error 必须处理并保留上下文；禁止吞错。
- 返回客户端的第三方响应或错误必须安全摘要，不能泄露 response body、token、完整 Content-Type 参数或嵌套 cause。
- 使用 `log.*Tag`、metrics provider 和 trace context；高基数字段不要作为指标标签。

## 数据层

- `none` 使用 `tgf.CacheModuleClose`；`redis` / `redis-mysql` 使用 `tgf.CacheModuleRedis`。
- write-behind 必须配置批量、重试、失败队列和启动重放；停机依赖框架终末 flush。
- 无真实 Redis/MySQL 时只运行 unit/compile 证据，不宣称 integration 已通过。

## 测试与交付

- 默认门：`go mod tidy`、`go test ./...`、`go build ./...`、`go vet ./...`、`tgfctl verify`。
- HTTP 冒烟必须发真实请求；长连接用 robot 或协议客户端；分布式必须真实启动 Consul/Redis 才算 integration。
- 记录精确 tgf 版本、profile 参数、已通过与未运行检查。修复最多三轮，同因重复失败就停止上报。
