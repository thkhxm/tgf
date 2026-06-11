# tgf

[![Go Report Card](https://goreportcard.com/badge/github.com/thkhxm/tgf)](https://goreportcard.com/report/github.com/thkhxm/tgf)
[![Go Version](https://img.shields.io/badge/go-1.26%2B-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**tgf** is a distributed game server framework written in Go that also makes
**HTTP web services a first-class citizen** alongside RPC. v2/v3 focus on
**stability, tooling, observability, API consistency, and HTTP web capability**,
letting small teams and solo developers focus on business logic instead of
connection storms, cross-node coordination, config hot reload, and metric
instrumentation.

> The primary documentation is in Chinese — see [`README.md`](README.md).
> This file is a brief English overview.

One framework covers three scenarios: a **conventional HTTP web service**
([`example/http_rest/`](example/http_rest/)), a **distributed web service**
that bridges HTTP to backend RPC ([`example/http_rpc/`](example/http_rpc/)),
and a **distributed game service** ([`example/single_process/`](example/single_process/)).

## Highlights

- 🌐 **HTTP first-class citizen** — `WithHTTPService` spins up a standard HTTP
  server with routing / middleware / rate limiting / auth / graceful shutdown;
  handlers bridge to backend RPC via `web.Backend` with end-to-end traceId.
  `WithHTTPServiceConsul` registers the HTTP service into Consul (discoverable /
  load-balanced); `WithClientOnly` runs a pure web/client process that does *not*
  masquerade as an RPC node
- 🛡️ **Stability** — Unified `IConn` gateway abstraction (TCP/WS/KCP),
  atomic cross-node login via Redis lock, reliable write-behind cache with
  compensation queue, KCP + AEAD gateway
- 🔧 **Tooling** — Makefile / golangci-lint / Dockerfile / GitHub Actions CI /
  CHANGELOG / dependency upgrades
- 📊 **Observability** — `tgf/metrics` and `tgf/trace` packages with zero
  external dependencies; plug in Prometheus / OpenTelemetry as needed
- 🎯 **RPC policy pipeline** — Per-method `MethodPolicy` with timeout,
  rate limiting, circuit breaker, and concurrency control
- 🔄 **Hot reload** — `component.ReloadGameConf` + `config.Reload` + fsnotify
  auto-watching
- 🏗️ **Builder unified** — `WithGatewayOptions` / `WithStandalone` /
  `WithSingleProcess` replace v1's parallel entry points

## One-liner: tgf v2 single-process mode

```go
rpc.NewRPCServer().
    WithSingleProcess().                              // zero Consul dependency
    WithService(new(UserService)).
    WithService(new(ShopService)).
    WithGatewayOptions(rpc.GatewayOptions{TCPPort: "8082"}).
    Run()
```

Business code `SendRPCMessage(ctx, Shop.Buy.NewRPC(req))` automatically
routes through an in-process reflection dispatcher. To migrate from
single-process prototype to distributed deployment, just drop the
`WithSingleProcess()` line.

## Quick start

See the "5-minute quick start" section in the main [`README.md`](README.md),
or run one of the 11 examples in [`example/`](example/):

```bash
cd example/single_process && go run .
```

Most examples do not require Redis / MySQL / Consul — `go run .` just works.

## Documentation

- [`README.md`](README.md) — Main documentation (Chinese)
- [`doc/architecture.md`](doc/architecture.md) — v2 architecture overview
- [`doc/migration-v1-to-v2.md`](doc/migration-v1-to-v2.md) — v1 → v2 migration guide
- [`doc/observability.md`](doc/observability.md) — Prometheus / OpenTelemetry integration
- [`CHANGELOG.md`](CHANGELOG.md) — Full changelog

## Links

- Repository: [github.com/thkhxm/tgf](https://github.com/thkhxm/tgf)
- API reference: [pkg.go.dev/github.com/thkhxm/tgf/v2](https://pkg.go.dev/github.com/thkhxm/tgf/v2)
- Project docs: [thkhxm.github.io/tgf_writerside](https://thkhxm.github.io/tgf_writerside/starter-topic.html)

## Contributing

1. Fork the repository
2. Create a `feature/xxx` branch
3. Add code + unit tests + example
4. Run `go test -race -count=1 ./...` to verify
5. Open a pull request

## Community

- **QQ group**: 7400585

## License

MIT License — see [LICENSE](LICENSE).
