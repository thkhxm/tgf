package rpc

import (
	"net/http"

	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/web"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G2：HTTP→RPC 桥的 rpc 侧接线——框架错误 → HTTP 状态码注册。
//
// web 包不 import rpc（防 import 环），所以"框架哨兵错误该映射成什么状态码"
// 由本文件在 init 里注册进 web 的错误映射表（web.RegisterErrorStatus，
// errors.Is 匹配）。这些错误全部由框架在调用方本地生成（策略管道拒绝 /
// 客户端超时 / 选路失败），不经过 rpcx 网络摊平，错误身份在分布式与单进程
// 两条路径上都保真——映射可靠。
//
// 消费方：web.RPC[...] 桥 handler 与业务 handler 里的 web.StatusFromError。
//2026/6/10
//***************************************************

func init() {
	// 限流拒绝（E1 策略管道 ErrRPCRateLimited）→ 429 Too Many Requests。
	web.RegisterErrorStatus(ErrRPCRateLimited, http.StatusTooManyRequests)
	// 并发超限（E1 ErrRPCOverload）→ 503（过载是服务端暂时性状态，可重试）。
	web.RegisterErrorStatus(ErrRPCOverload, http.StatusServiceUnavailable)
	// 熔断开路（E1 ErrRPCCircuitOpen）→ 503（下游被隔离，等恢复窗口）。
	web.RegisterErrorStatus(ErrRPCCircuitOpen, http.StatusServiceUnavailable)
	// RPC 超时（A7/E1 resolveRPCTimeout 触发）→ 504 Gateway Timeout。
	web.RegisterErrorStatus(tgf.ErrorRPCTimeOut, http.StatusGatewayTimeout)
	// 目标模块无可用节点 / client 未初始化（滚动发布、client-only 后端全挂）
	// → 503（服务暂不可用，可重试；区别于 502 的"后端返回了错误"）。
	web.RegisterErrorStatus(tgf.ServiceNotFound, http.StatusServiceUnavailable)
}
