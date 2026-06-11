# tgf

> 中文主文档见 [`README.md`](README.md)，本文件是简要中文概览。

**tgf** 是一套基于 Go 的分布式游戏服务器框架，同时把 **HTTP web 服务**做成与
RPC 平级的一等公民。v2/v3 聚焦稳定性、工程化、可观测性、API 一致性与 HTTP web
能力，让中小型团队和独立开发者可以**只关注业务逻辑**。

一套框架覆盖三类场景：**常规 http web 服务**（[`example/http_rest/`](example/http_rest/)）、
**分布式 web 服务**（[`example/http_rpc/`](example/http_rpc/)）、**分布式游戏服务**
（[`example/single_process/`](example/single_process/)）。

## 一句话看 tgf v2

```go
rpc.NewRPCServer().
    WithSingleProcess().                              // 零 Consul 单进程模式
    WithService(new(UserService)).
    WithService(new(ShopService)).
    WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).
    Run()
```

业务代码 `SendRPCMessage(ctx, Shop.Buy.NewRPC(req))` 自动走**进程内反射调用**，
从单进程原型迁移到分布式部署只需要去掉 `WithSingleProcess()` 一行。

## 主要能力

- 🌐 **HTTP 一等公民**：`WithHTTPService` 起标准 HTTP 服务，路由 / 中间件 / 限流 / 鉴权 / 优雅停机齐全，handler 经 `web.Backend` 桥调后端 RPC（traceId 全链路）；`WithHTTPServiceConsul` 把 HTTP 服务注册进 Consul（可被发现/负载均衡），`WithClientOnly` 起"不伪装成 RPC 节点"的纯 web/client 接入进程
- 🛡️ **稳定性**：网关 `IConn` 统一抽象、跨节点登录原子化、write-behind 落库可靠性、KCP+AEAD 网关
- 🔧 **工程化**：Makefile / golangci / CI / Dockerfile / CHANGELOG / 依赖升级
- 📊 **可观测性**：`tgf/metrics` + `tgf/trace` 零依赖接口，按需接 Prometheus / OpenTelemetry
- 🎯 **策略化 RPC**：限流 / 熔断 / 并发 / 超时一套 `MethodPolicy`
- 🔄 **热更**：`component.ReloadGameConf` + `config.Reload` + fsnotify
- 🏗️ **Builder 统一**：`WithGatewayOptions` / `WithStandalone` / `WithSingleProcess`

## 快速上手

见主文档 [`README.md`](README.md) 的"5 分钟快速上手"章节。
可以直接跑 [`example/`](example/) 下的 11 个示例项目：

```bash
cd example/single_process && go run .
```

## 文档

- [`README.md`](README.md) — 主文档（中文）
- [`doc/architecture.md`](doc/architecture.md) — v2 架构总览
- [`doc/migration-v1-to-v2.md`](doc/migration-v1-to-v2.md) — v1 → v2 迁移指南
- [`doc/observability.md`](doc/observability.md) — 可观测性接入
- [`CHANGELOG.md`](CHANGELOG.md) — 变更日志

## 外部链接

- 项目地址：[github.com/thkhxm/tgf](https://github.com/thkhxm/tgf)
- API 参考：[pkg.go.dev/github.com/thkhxm/tgf/v2](https://pkg.go.dev/github.com/thkhxm/tgf/v2)
- 项目文档：[thkhxm.github.io/tgf_writerside](https://thkhxm.github.io/tgf_writerside/starter-topic.html)
- 国内镜像：[tgf.yamigame.net:8080](http://tgf.yamigame.net:8080/)

## 交流群

**QQ 群：7400585**

## License

MIT License — 见 [LICENSE](LICENSE)。
