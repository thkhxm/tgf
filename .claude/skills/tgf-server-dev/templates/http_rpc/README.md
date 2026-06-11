# {{PROJECT_NAME}} —— tgf v2 混合服务（HTTP 入口 + 游戏服 RPC）

对外 HTTP/REST 入口 + 后端游戏 service，经框架 **HTTP→RPC 桥**（`web.Backend`）连接：
HTTP 与游戏客户端共用同一套 service 实现、同一条策略管道（限流/熔断/超时）、
同一份 metrics；traceId（`X-Trace-Id`）从 HTTP 全链路透传到后端 RPC。

## 运行（单进程混合部署，零外部依赖）

```bash
go mod tidy
go run .
```

验证：

```bash
curl -i http://127.0.0.1:8091/health
curl -i http://127.0.0.1:8091/players/p001
# → 200 {"PlayerId":"p001","Level":1}
curl -i -X POST http://127.0.0.1:8091/players/p001/gift -d '{"giftId":"welcome"}'
# → 200 {"OK":true,"Message":"玩家 p001 已领取礼包 welcome"}
curl -i -H "X-Trace-Id: my-trace-1" http://127.0.0.1:8091/players/p001
# → 响应头回写同一个 X-Trace-Id，后端日志同 traceId
```

## 拆分多进程（生产分布式形态）

业务代码（service 实现、handler、Invoke 调用点）**零改动**，只改装配：

- **游戏服进程**：去掉 `WithSingleProcess()`，正常 `WithService(...).Run()`
  注册进 Consul（参考 distributed 模板的 cmd/game）；
- **HTTP 接入层进程**：

  ```go
  rpc.NewRPCServer().
      WithClientOnly().                       // 不伪装成 RPC 节点：不监听 rpcx 端口、不注册 service，
                                              // 但照常初始化 Consul discovery + RPC client
      WithHTTPServiceConsul(web.Options{      // HTTP 实例本身注册进 Consul（health 路由 + TTL 续约）
          Routes: routes,
      }, rpc.HTTPRegistration{
          ServiceName: "{{PROJECT_NAME}}-web",
      }).
      Run()
  ```

  此时 `backend.Invoke` 自动走分布式路径（rpcx + Consul 发现）找到游戏服。
  注意：**默认 Run() 即使不 WithService 也会监听 ServicePort 并注册进 Consul**，
  那不是 client-only——接入层必须显式 `WithClientOnly()`。

## 要点

- handler 内统一 `web.BackendFromRequest(r)` 取桥，`backend.Invoke(ctx, module, method, &args, &reply)`
  调后端（args/reply 为指针，语义同 rpcx 方法签名）；
- 后端调用失败统一回 502（上游网关语义），客户端可重试；
- 方法策略 `WithMethodPolicy("player.GiveGift", ...)` 对 HTTP 桥与游戏客户端 RPC 同时生效。
