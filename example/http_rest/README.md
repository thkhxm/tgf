# http_rest —— 纯 REST API 示例（常规 http web 服务）

把 tgf 当一个**普通 HTTP web 框架**用：不接任何游戏 RPC 后端，只用
`WithHTTPService` 起一个标准 REST 服务，展示 HTTP 一等公民能力的基础面。

## 运行

```bash
cd tgf/example/http_rest
go run .
```

不需要 Consul / Redis / MySQL（`WithStandalone()` + `WithCache(tgf.CacheModuleClose)`）。

## 试一试

```bash
curl -i http://127.0.0.1:8090/health
curl -i http://127.0.0.1:8090/users/42
curl -i -X POST http://127.0.0.1:8090/users -d '{"name":"tim"}'

# 鉴权分组：无 token → 401，带对的 token → 200
curl -i http://127.0.0.1:8090/admin/stats
curl -i -H "Authorization: Bearer s3cr3t" http://127.0.0.1:8090/admin/stats
```

每个响应都带 `X-Trace-Id`（Trace 中间件注入）与 `Server: tgf-web`（自定义中间件）。

## 演示的能力

| 能力 | 代码位置 | 说明 |
|------|---------|------|
| `WithHTTPService(web.Options{...})` | main.go `main()` | HTTP 与 RPC 平级的服务构建器 |
| 路由 + 路径参数 | `r.GET("/users/{id}", ...)` | Go 1.22 ServeMux 语义，`req.PathValue("id")` |
| 内置中间件链 | 默认全开 | Trace / AccessLog / Metrics / Recover |
| 全局限流 | `Limiter: web.NewTokenBucketLimiter(1000)` | E1 同款令牌桶，超限自动 429 |
| 自定义中间件 | `Middlewares: []web.Middleware{serverHeader}` | 与 net/http 生态同构 |
| 鉴权分组 | `r.Group("/admin", web.Auth(web.StaticBearerToken(...)))` | constant-time 比对，fail-closed |
| 优雅停机 | `Run()` + 信号 + 框架 `Destroy` 链 | 与 RPC 共享 D3，`http.Server.Shutdown` drain |

## 端口与配置

`Addr: ":8090"` 是显式写死的；省略 `Addr` 则读配置项 `HTTPPort`（默认 8090）。
读请求头超时 / 优雅停机超时同样可经配置项 `HTTPReadHeaderTimeoutSec` /
`HTTPShutdownTimeoutSec` 调整（见 `tgf/config.HTTPConfig`）。
