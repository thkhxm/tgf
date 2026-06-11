# {{PROJECT_NAME}} —— tgf v2 分布式游戏服

基于 [tgf v2](https://github.com/thkhxm/tgf) 的分布式游戏服务器骨架：

```
{{PROJECT_NAME}}/
├── cmd/
│   ├── gateway/main.go   # 网关进程：客户端 TCP/WS/KCP 入口 + 路由
│   └── game/main.go      # 业务进程：user 等 Module，注册进 Consul
├── internal/
│   ├── api/              # RPC 契约（出入参 + ServiceAPI 描述符）——调用方/实现方共同依赖
│   └── service/          # Module 实现（rpcx service）
├── deploy/               # Dockerfile / docker-compose / K8s（由脚手架 deploy 模板拷入）
├── .env.dev              # 开发环境配置（敏感项不入库）
└── go.mod
```

```
客户端 ──TCP/WS/KCP──▶ gateway 进程 ──rpcx(Consul 发现)──▶ game 进程(user 模块)
                          │                                  │
                          └────────── Redis（用户路由表 / 业务缓存）
```

## 本地运行

```bash
# 1. 起依赖（Consul + Redis）
docker compose -f deploy/docker-compose.yml up -d consul redis

# 2. 起业务进程与网关进程（两个终端，都在项目根执行）
go run ./cmd/game
go run ./cmd/gateway
```

预期：

- game 终端输出 `UserService 启动`、`game 已启动`，Consul UI（http://127.0.0.1:8500）
  的 KV `/tgf` 下可见 user 节点；
- gateway 终端输出 `gateway 已启动  TCP Gateway : localhost:8082`；
- `Ctrl+C` 任一进程：先从 Consul 摘除，再停 accept、drain、终末落库。

## 端口规划

| 端口 | 进程 | 用途 |
|------|------|------|
| 8082 | gateway | 客户端 TCP 入口（`GatewayOptions.TCPPort`） |
| 8081 | gateway | rpcx 内部端口（`.env.dev` 的 `ServicePort`） |
| 20000-20100 | game | rpcx 内部端口（`WithRandomServicePort`，同机多实例不冲突） |

## 接入客户端协议（必读）

骨架内 `user.Login` / `user.GetRole` 是**服务间 RPC** 风格（普通 struct 参数），
只能被 `rpc.SendRPCMessage` 调用。要让客户端帧经网关直达：

1. 定义业务 `.proto`，`protoc --go_out=.` 生成消息代码；
2. 方法签名改为 `(ctx, *rpc.Args[*pb.XxxReq], *rpc.Reply[*pb.XxxRes]) error`
   （样例见 `internal/service/user.go` 文件尾的注释）；
3. 登录链路（框架默认鉴权 fail-closed）：
   - 业务验证账号 → `rpc.GenerateLoginToken(userId, 12*time.Hour)` 签发 token；
   - 客户端 `gate.Login` 帧携带 token（服务侧辅助 `rpc.UserLoginWithToken`）；
   - 密钥配置在 `.env.dev` 的 `LoginTokenSecret`（所有进程一致）；
4. 登录前可调用的方法（如 user.Login 本身）在 gateway 进程加白名单：
   `WithWhiteService("user.Login")`；
5. 联调客户端用框架自带 robot：`robot.NewRobotTcp()` / `NewRobotWs("/ws")` /
   `NewRobotKCP(aeadKey)`（参考 tgf 仓库 `example/robot_test/`）。

## KCP 接入（实时同步场景）

```go
// gateway main.go —— 密钥必须 32 字节；不设即明文（仅限开发/内网），长度错误启动期 panic
key, _ := hex.DecodeString(os.Getenv("KCP_AEAD_KEY_HEX")) // 业务自有键，godotenv 已注入
WithGatewayOptions(rpc.GatewayOptions{
    TCPPort: "8082",
    KCP:     rpc.NewKCPBuilder("8300").WithAEADKey(key),
})
```

## 数据层（Redis + MySQL write-behind）

```go
import "github.com/thkhxm/tgf/v2/db"

type RoleData struct {
    db.Model
    Uid   string `orm:"pk"`
    Level int
}
func (r RoleData) GetTableName() string { return "t_role" }

cache := db.NewAutoCacheBuilder[string, *RoleData]().
    WithMemCache(3600).                    // 内存热层（秒）
    WithAutoCache("role", 24*time.Hour).   // Redis 层
    WithLongevityCache(5 * time.Second).   // MySQL write-behind 周期落库
    WithLongevityFailureQueue(queue).      // 落库失败补偿队列（重放见 db.ReplayFailureQueue）
    New()
```

`.env.dev` 打开 MySql* 配置后生效；优雅停机时框架做终末 flush，配合补偿队列保证不丢脏数据。

## 部署

`deploy/` 模板：`docker-compose.yml`（app + Consul + Redis + MySQL 编排）、
`Dockerfile`（多阶段构建，`--build-arg MAIN_PKG=./cmd/gateway` 选进程）、
`k8s.yaml`（SIGTERM 优雅停机 + Consul TTL 自动摘除已内建，直接可用）。
