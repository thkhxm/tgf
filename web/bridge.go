package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/thkhxm/tgf/trace"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G2：HTTP→RPC 桥——
//  1. 请求体 ↔ RPC Args/Reply 的 JSON 编解码 helper（DecodeJSON / WriteJSON）；
//  2. 错误码映射（RPC error → HTTP status）：StatusFromError + 可注册的哨兵
//     错误表（RegisterErrorStatus，rpc 包在 init 里把限流/熔断/超时/无节点
//     等框架错误注册成 429/503/504）+ 业务自定义状态（WithHTTPStatus）；
//  3. 泛型桥 handler（RPC[Req,Res]）与 handler 内便捷调用（Invoke[Req,Res]）：
//     一行把 "POST /api/x + JSON body" 桥到后端 module.method。
//
// traceId 透传不需要本文件做任何事：Trace 中间件已把 traceId 注入请求
// context，Backend（rpc 侧 defaultWebBackend）会把它写进 rpcx
// ReqMetaData["TraceId"]——HTTP 请求 → 桥 → 后端 RPC 全链路同一个 traceId；
// 错误响应（WriteError）会把该 traceId 回带给客户端便于排障关联。
//
// 本文件遵守 web 包的自包含约束：不 import rpc，所有后端调用都走
// Backend 注入接口（见 web.go）。
//2026/6/10
//***************************************************

// ContentTypeJSON 是桥接响应统一使用的 Content-Type。
const ContentTypeJSON = "application/json; charset=utf-8"

// DefaultMaxBodyBytes 是 DecodeJSON 默认的请求体大小上限（防巨帧 OOM，
// 与网关 64KB 单帧上限同向但放宽到 web 常规体量）。
const DefaultMaxBodyBytes int64 = 4 << 20 // 4 MiB

// ErrBackendNotConfigured 表示请求 context 上没有注入 Backend——
// 通常意味着 web.Server 没经 rpc.WithHTTPService 装配且 Options.Backend 为 nil。
// StatusFromError 把它映射为 503（部署/装配问题，不是客户端的错）。
var ErrBackendNotConfigured = errors.New("tgf/web: rpc backend not configured")

// ErrBodyTooLarge 表示请求体超过解码上限。映射为 413。
var ErrBodyTooLarge = errors.New("tgf/web: request body too large")

// ---- 请求体/响应体 编解码 ----

// DecodeJSON 把请求体 JSON 解码到 v（v 必须是指针）。
//   - 空 body（GET / 无参 POST）视为合法：v 保持零值，返回 nil；
//   - body 超过 DefaultMaxBodyBytes 返回 ErrBodyTooLarge；
//   - 非法 JSON 返回解码错误（桥 handler 映射为 400）。
func DecodeJSON(r *http.Request, v any) error {
	return DecodeJSONLimit(r, v, DefaultMaxBodyBytes)
}

// DecodeJSONLimit 同 DecodeJSON，但可自定义体积上限（limit <= 0 时用默认值）。
func DecodeJSONLimit(r *http.Request, v any, limit int64) error {
	if r.Body == nil {
		return nil
	}
	if limit <= 0 {
		limit = DefaultMaxBodyBytes
	}
	// 多读 1 字节用于检测"恰好超限"：读满 limit+1 说明 body > limit。
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return fmt.Errorf("tgf/web: 读取请求体失败: %w", err)
	}
	if int64(len(data)) > limit {
		return ErrBodyTooLarge
	}
	if len(data) == 0 {
		return nil // 空 body：参数保持零值
	}
	if err = json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("tgf/web: 请求体 JSON 解码失败: %w", err)
	}
	return nil
}

// WriteJSON 以 status 写出 v 的 JSON 编码（自动设置 Content-Type）。
// 编码失败时（极少：v 含不可序列化字段）回退 500 纯文本——绝不静默吞错。
func WriteJSON(w http.ResponseWriter, status int, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal server error: response encode failed", http.StatusInternalServerError)
		return fmt.Errorf("tgf/web: 响应 JSON 编码失败: %w", err)
	}
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	_, werr := w.Write(data)
	return werr
}

// ErrorResponse 是桥接错误的统一响应 envelope。
// TraceId 来自 Trace 中间件（与后端 RPC 链路同一个 id），客户端拿它报障，
// 运维直接全链路检索日志。
type ErrorResponse struct {
	Code    int    `json:"code"`              // 同 HTTP status（冗余进 body 便于不读响应头的客户端）
	Message string `json:"message"`           // 错误描述
	TraceId string `json:"traceId,omitempty"` // 链路 id（X-Trace-Id）
}

// WriteError 写出统一的 JSON 错误响应（带 traceId）。
func WriteError(w http.ResponseWriter, r *http.Request, status int, message string) {
	_ = WriteJSON(w, status, &ErrorResponse{
		Code:    status,
		Message: message,
		TraceId: trace.TraceIDFromContext(r.Context()),
	})
}

// ---- 错误码映射（RPC error → HTTP status）----

// HTTPStatusCarrier 是"自带 HTTP 状态码"的错误接口。
// 业务错误实现它（或直接用 WithHTTPStatus 包装）即可精确控制桥接响应码。
// 注意：跨 rpcx 网络传输的错误会被摊平成字符串（rpcx ServiceError），
// 接口信息只在"框架本地生成的错误"（策略拒绝/超时/无节点）与单进程直通
// 路径上保真——业务需要跨网络的精确错误码时应在 Reply 里带业务 code 字段。
type HTTPStatusCarrier interface {
	HTTPStatus() int
}

// statusError 是 WithHTTPStatus 的载体：保留原错误链（Unwrap）。
type statusError struct {
	err    error
	status int
}

func (e *statusError) Error() string   { return e.err.Error() }
func (e *statusError) Unwrap() error   { return e.err }
func (e *statusError) HTTPStatus() int { return e.status }

// WithHTTPStatus 把 err 包装为携带 HTTP 状态码的错误（保留 errors.Is/As 链）。
// err 为 nil 返回 nil。
func WithHTTPStatus(err error, status int) error {
	if err == nil {
		return nil
	}
	return &statusError{err: err, status: status}
}

// errStatusEntry 哨兵错误 → 状态码的注册表条目。
type errStatusEntry struct {
	target error
	status int
}

var (
	errStatusMu    sync.RWMutex
	errStatusTable []errStatusEntry
)

func init() {
	// web 包自身哨兵的默认映射（rpc 侧的框架哨兵在 rpc/http_bridge.go init 注册）：
	// Backend 未装配是部署/装配问题 → 503；请求体超限 → 413。
	RegisterErrorStatus(ErrBackendNotConfigured, http.StatusServiceUnavailable)
	RegisterErrorStatus(ErrBodyTooLarge, http.StatusRequestEntityTooLarge)
}

// RegisterErrorStatus 注册一条"哨兵错误 → HTTP 状态码"映射（errors.Is 匹配）。
// 框架侧的真实注册点在 rpc 包 init（限流 429 / 熔断 503 / 超时 504 / 无节点
// 503，见 rpc/http_bridge.go）；业务也可注册自己的哨兵。
// 后注册的优先（业务可覆盖框架默认）。
func RegisterErrorStatus(target error, status int) {
	if target == nil || status < 100 || status > 599 {
		return
	}
	errStatusMu.Lock()
	defer errStatusMu.Unlock()
	// prepend：后注册的排在前面，查表时先命中。
	errStatusTable = append([]errStatusEntry{{target: target, status: status}}, errStatusTable...)
}

// StatusFromError 把一次后端调用的 error 映射为 HTTP 状态码：
//  1. nil → 200；
//  2. 错误链上携带 HTTPStatusCarrier（WithHTTPStatus / 业务自实现）→ 取其值；
//  3. 命中 RegisterErrorStatus 注册表（errors.Is）→ 取注册值；
//  4. context.DeadlineExceeded → 504（调用超时）；
//  5. 其余 → 502 Bad Gateway（后端调用失败的通用语义）。
func StatusFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var carrier HTTPStatusCarrier
	if errors.As(err, &carrier) {
		return carrier.HTTPStatus()
	}
	errStatusMu.RLock()
	table := errStatusTable
	errStatusMu.RUnlock()
	for _, e := range table {
		if errors.Is(err, e.target) {
			return e.status
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

// ---- 桥 handler / 桥调用 ----

// Invoke 是 handler 内调用后端 RPC 的便捷入口：从请求 context 取 Backend，
// 分配 Reply 并发起同步调用。Req/Res 用值类型实参（与后端 service 方法
// (ctx, *Req, *Res) error 的指针形参对应）。
//
//	res, err := web.Invoke[GetUserReq, GetUserRes](r, "user", "GetUser",
//	    &GetUserReq{Id: r.PathValue("id")})
//	if err != nil {
//	    web.WriteError(w, r, web.StatusFromError(err), err.Error())
//	    return
//	}
//	_ = web.WriteJSON(w, http.StatusOK, res)
func Invoke[Req any, Res any](r *http.Request, module, method string, args *Req) (*Res, error) {
	backend, ok := BackendFromRequest(r)
	if !ok || backend == nil {
		return nil, ErrBackendNotConfigured
	}
	if args == nil {
		args = new(Req)
	}
	reply := new(Res)
	if err := backend.Invoke(r.Context(), module, method, args, reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// RPC 构造一个"JSON in → 后端 module.method → JSON out"的桥 handler：
//   - 请求体 JSON 解码为 Req（空 body = 零值；非法 JSON → 400；超限 → 413）；
//   - 经注入的 Backend 调用后端（traceId / 限流熔断 / 超时 / metrics 全部
//     由 Backend 与中间件链生效）；
//   - 成功 → 200 + Res 的 JSON；失败 → StatusFromError 映射状态码 + 统一
//     ErrorResponse envelope（带 traceId）。
//
// 用法（路由注册即声明完一个 REST→RPC 端点）：
//
//	r.POST("/api/user", web.RPC[GetUserReq, GetUserRes]("user", "GetUser"))
//
// 需要路径/查询参数参与构造 Req 的端点，用 Invoke 手写 handler。
func RPC[Req any, Res any](module, method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backend, ok := BackendFromRequest(r)
		if !ok || backend == nil {
			WriteError(w, r, http.StatusServiceUnavailable, ErrBackendNotConfigured.Error())
			return
		}
		args := new(Req)
		if err := DecodeJSON(r, args); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ErrBodyTooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			WriteError(w, r, status, err.Error())
			return
		}
		reply := new(Res)
		if err := backend.Invoke(r.Context(), module, method, args, reply); err != nil {
			WriteError(w, r, StatusFromError(err), err.Error())
			return
		}
		_ = WriteJSON(w, http.StatusOK, reply)
	}
}
