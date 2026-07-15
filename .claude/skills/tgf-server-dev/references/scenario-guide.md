# 已知场景指南

所有场景先从四个受测 profile 之一开始，不另建手写模板。框架仓库 `example/README.md` 是证据矩阵，
必须区分 unit、compile 和真实 integration。

| 场景 | 生成/扩展方式 | 最低验证 |
| --- | --- | --- |
| single-process-game | `single` | service unit + 启动/停机 |
| distributed-game | `distributed` | 双进程 compile；真实 Consul/Redis 才算 integration |
| http-rest | `http-rest` | handler unit + 真 HTTP 冒烟 |
| http-rpc | `http-rpc` | service/handler unit + 真 HTTP→RPC 冒烟 |
| gateway-tcp | 游戏 profile `protocol=tcp` | TCP robot 连接 |
| gateway-ws | 游戏 profile `protocol=websocket|all` | WebSocket robot 连接 |
| gateway-kcp | 游戏 profile `protocol=kcp|all` | 32 字节 key + KCP robot 连接 |
| robot-self-test | 参考 `example/robot_test` | TCP/WS/KCP 选择的协议真实连接 |
| redis-mysql-write-behind | `data=redis-mysql` | failure queue unit；真实 Redis/MySQL 后验证落库 |
| config-reload | 最近 profile + `config.OnReload` | 好配置替换、坏配置保旧值 |
| game-config | 游戏 profile + `WithGameConfig` | JSON load/reload/watcher |
| logging | 任一 profile | tag/structured log，确认无 secret |
| metrics-trace | 任一 profile + provider | trace 透传、指标计数与低基数标签 |
| rpc-policy | 有 service 的 profile + `WithMethodPolicy` | 超时/限流/并发策略单测 |
| util | 任一 profile | 对使用的 util 写确定性单测 |
| platform SDK | HTTP 或游戏 profile + `WithPlatform` | fake 合约单测；真实凭据在线验证单独记录 |

平台实现来自 `github.com/thkhxm/tgf-platform/*` 独立 module。选择具体平台时先确认对应公共 tag 已发布；
本地 sibling replace 只用于平台仓源码联调，不能进入业务项目。
