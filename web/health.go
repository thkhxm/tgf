package web

import (
	"net/http"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G2/G3：HTTP health endpoint——
// 分布式 web 服务的探活端点：Consul HTTP check、k8s liveness/readiness、
// 外部负载均衡（nginx/traefik）都打它。rpc.Server.WithHTTPServiceConsul
// 默认把它自动挂到 DefaultHealthPath 并随服务注册进 Consul。
//2026/6/10
//***************************************************

// DefaultHealthPath 是 health endpoint 的默认路径。
// rpc 侧的 Consul 注册（HTTPRegistration.HealthPath 为空时）与这里保持一致。
const DefaultHealthPath = "/health"

// healthOKBody 探活成功的固定响应体（JSON，便于人读与机器解析）。
var healthOKBody = []byte(`{"status":"ok"}`)

// Health 返回一个无条件 200 的探活 handler：进程活着、HTTP 栈能完成
// 收包-路由-回包即算健康（这正是"端口活性"级别探活的语义）。
func Health() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(healthOKBody)
	}
}

// HealthFunc 返回一个带业务自检的探活 handler：check 返回 nil → 200；
// 返回 error → 503 + 统一错误 envelope（探活方据此摘流量）。
// check 为 nil 时退化为 Health() 的无条件 200。
// 典型自检：关键依赖连通性（Redis/MySQL ping）、后端 RPC client 可用性。
func HealthFunc(check func() error) http.HandlerFunc {
	if check == nil {
		return Health()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if err := check(); err != nil {
			WriteError(w, r, http.StatusServiceUnavailable, "unhealthy: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(healthOKBody)
	}
}
