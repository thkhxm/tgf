# {{PROJECT_NAME}} —— tgf v2 单进程游戏服

基于 [tgf v2](https://github.com/thkhxm/tgf) 的单进程游戏服务器：全部业务 Module +
客户端网关（TCP）跑在一个进程里，**零 Consul / 零 Redis / 零 MySQL 外部依赖**。

## 运行

```bash
go mod tidy
go run .
```

预期输出（节选）：

```
========================================
  {{PROJECT_NAME}} 单进程游戏服已启动
  TCP Gateway : localhost:8082
  运行环境    : dev
  Modules     : user, shop
========================================

[冒烟] Login 成功: 欢迎 player_001，新手礼包已发放！
```

`Ctrl+C` 触发优雅停机（停 accept → drain → 终末落库）。

## 端口

| 端口 | 用途 | 改法 |
|------|------|------|
| 8082 | 客户端 TCP 网关 | main.go 的 `GatewayOptions.TCPPort` |
| 8081 | rpcx 内部服务端口 | `.env.dev` 的 `ServicePort` |

## 接入客户端协议（让 TCP/WS/KCP 客户端帧直达业务方法）

骨架里的方法是"服务间 RPC"风格（普通 struct 参数），客户端帧不可直达。
客户端可达的方法参数必须是 `rpc.Args[T]` / `rpc.Reply[T]`（T 为 protobuf Message）：

1. 写业务 `.proto` 并生成 Go 代码（`protoc --go_out=. xxx.proto`）；
2. 方法签名改为：

   ```go
   func (s *UserService) Hello(ctx context.Context,
       args *rpc.Args[*pb.HelloReq], reply *rpc.Reply[*pb.HelloRes]) error {
       req := args.GetData()
       // ...业务逻辑...
       return reply.SetData(&pb.HelloRes{Msg: "hi " + req.Name})
   }
   ```

3. 客户端登录：框架默认开启登录鉴权（fail-closed）。业务登录服务验证账号后用
   `rpc.GenerateLoginToken(userId, ttl)` 签发 token 下发客户端；客户端 `gate.Login`
   帧带 token（服务侧辅助入口 `rpc.UserLoginWithToken(ctx, userId, token)`）。
   密钥在 `.env.dev` 的 `LoginTokenSecret` 配置；
4. 无需登录即可调用的方法用 `WithWhiteService("module.Method")` 加白名单；
5. 联调用框架自带 robot 客户端：`robot.NewRobotTcp()` / `NewRobotWs("/ws")` /
   `NewRobotKCP(aeadKey)`（参考 tgf 仓库 `example/robot_test/`）。

## 加 WebSocket / KCP

```go
WithGatewayOptions(rpc.GatewayOptions{
    TCPPort: "8082",
    WSPath:  "/ws",                                        // WebSocket（同端口升级）
    KCP:     rpc.NewKCPBuilder("8300").WithAEADKey(key32), // KCP，key32 为 32 字节预共享密钥
})
```

KCP 的 AEAD 密钥不设即明文（仅限开发/内网）；长度非 32 字节会在启动期 panic（fail-fast）。

## 升级为分布式

1. 去掉 `WithSingleProcess()`（默认模式自动挂 Consul，`.env.dev` 配 `ConsulAddress`）；
2. 把 Module 拆成独立进程（参考脚手架 distributed 模板）。

**ServiceAPI 定义、方法签名、SendRPCMessage 调用点全部不变。**
