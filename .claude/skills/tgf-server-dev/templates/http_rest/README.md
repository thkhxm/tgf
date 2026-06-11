# {{PROJECT_NAME}} —— tgf v2 纯 HTTP REST 服务

把 [tgf v2](https://github.com/thkhxm/tgf) 当常规 web 框架用：标准 REST 路由 +
内置中间件链（Trace / AccessLog / Metrics / Recover）+ 限流 + Bearer 鉴权 + 优雅停机。
**零 Consul / 零 Redis / 零 MySQL 外部依赖。**

## 运行

```bash
go mod tidy
go run .
```

验证：

```bash
curl -i http://127.0.0.1:8090/health                 # 200 ok
curl -i http://127.0.0.1:8090/users/42               # 200 {"id":0,"name":"user-42"}
curl -i -X POST http://127.0.0.1:8090/users -d '{"name":"tim"}'   # 201
curl -i http://127.0.0.1:8090/admin/stats            # 401（无口令）
curl -i -H "Authorization: Bearer <ADMIN_TOKEN>" http://127.0.0.1:8090/admin/stats  # 200
```

每个响应带 `X-Trace-Id` 头（客户端可传入同名头实现链路透传）。
`Ctrl+C` 触发优雅停机（drain in-flight 请求后退出）。

## 要点

- 端口、超时读 `.env.dev`（`HTTPPort` 等），代码内不硬编码；
- `/admin/*` 口令读 `config.Current().Security.AdminToken`（`ADMIN_TOKEN`），
  经 provider 每次请求动态读取，`config.Reload()` 后即热轮换；
  **不配置则 503 fail-closed**——这是框架的默认安全语义，不要绕过；
- 自定义中间件与 `net/http` 同构（`func(http.Handler) http.Handler`），
  填 `web.Options.Middlewares` 追加在内置链之后；
- 要把本服务注册进 Consul 供发现/负载均衡（多实例网关型部署）：
  `WithStandalone()` 改为 `WithClientOnly()` 并加
  `WithHTTPServiceConsul(opt, rpc.HTTPRegistration{ServiceName: "{{PROJECT_NAME}}-web"})`；
- 要从 handler 调后端游戏服 RPC → 用 http_rpc 模板（`web.BackendFromRequest` 桥）。
