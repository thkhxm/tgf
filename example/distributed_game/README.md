# distributed_game —— 真实分布式网关 + 业务服

该示例是两个独立进程，不是 `single_process` 的换名：

```text
cmd/gateway  -- Consul/rpcx -->  cmd/game
    |                              |
TCP / WS / KCP                internal/service.UserService
```

业务布局严格分为 `cmd/gateway`、`cmd/game`、`internal/api` 和
`internal/service`；两个进程都使用当前 module
`github.com/thkhxm/tgf/v2`。

## 外部依赖

- Consul：网关和 game 的服务发现。
- Redis：网关的用户→节点路由。
- 32 字节 KCP AEAD 密钥：只通过运行环境注入，不入库。

## 运行

1. 启动 Consul 和 Redis。
2. 复制并填写配置：

```powershell
Copy-Item .env.game.example .env.game
Copy-Item .env.gateway.example .env.gateway
```

3. 在终端 A 启动业务服：

```powershell
$env:TGFMODULE='game'
go run ./cmd/game
```

4. 在终端 B 注入密钥并启动网关：

```powershell
$env:TGFMODULE='gateway'
$env:TGF_EXAMPLE_KCP_AEAD_KEY_HEX='<64 hex characters>'
go run ./cmd/gateway
```

网关端口：TCP `:8082`、WebSocket `ws://127.0.0.1:8082/ws`、KCP
`:8300`。`SIGINT/SIGTERM` 由 tgf 处理，两个 `main` 只等待 `Run()`
返回的 done。

## 证据边界

- `go test -count=1 ./example/distributed_game/...`：编译两个进程，并在无
  Consul/Redis 时单测业务 handler 与 KCP 密钥校验。
- 真实跨进程发现、网关连接和 Redis 路由属于外部集成验证；
  本仓默认 `go test` 不会启动或伪装它们。
