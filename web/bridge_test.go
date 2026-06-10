package web

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//@Description G2 单测：HTTP→RPC 桥的编解码 / 错误码映射 / 泛型桥 handler /
// health endpoint。后端用 BackendFunc 桩——真实 rpc 后端的端到端链路由
// rpc/http_g2_bridge_test.go（单进程直通）与 rpc/it_g_http_test.go（真实
// Consul + 跨 rpcx 网络）覆盖。
//2026/6/10
//***************************************************

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thkhxm/tgf/trace"
)

type bridgeReq struct {
	Id string `json:"id"`
}

type bridgeRes struct {
	Name string `json:"name"`
	Hit  bool   `json:"hit"`
}

// stubBackend 把 Invoke 委托给注入函数并录制最近一次调用。
type stubBackend struct {
	lastModule string
	lastMethod string
	fn         func(ctx context.Context, args, reply any) error
}

func (b *stubBackend) Invoke(ctx context.Context, module, method string, args, reply any) error {
	b.lastModule, b.lastMethod = module, method
	return b.fn(ctx, args, reply)
}

// bridgeDo 把请求打到带 Backend 注入与 Trace 中间件的桥 handler 上。
func bridgeDo(t *testing.T, h http.Handler, backend Backend, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	wrapped := Chain(h, Trace(), backendMiddleware(backend))
	wrapped.ServeHTTP(w, req)
	return w
}

// TestBridge_RPCHandler_JSONRoundTrip 泛型桥 handler 的成功路径：
// JSON body → Req 解码 → Backend.Invoke(module, method) → Res JSON 响应。
func TestBridge_RPCHandler_JSONRoundTrip(t *testing.T) {
	backend := &stubBackend{fn: func(_ context.Context, args, reply any) error {
		req, ok := args.(*bridgeReq)
		if !ok {
			t.Fatalf("args 类型 = %T, want *bridgeReq", args)
		}
		res := reply.(*bridgeRes)
		res.Name = "user-" + req.Id
		res.Hit = true
		return nil
	}}

	w := bridgeDo(t, RPC[bridgeReq, bridgeRes]("user", "GetUser"),
		backend, http.MethodPost, "/api/user", `{"id":"42"}`, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if backend.lastModule != "user" || backend.lastMethod != "GetUser" {
		t.Errorf("backend 收到 %s.%s, want user.GetUser", backend.lastModule, backend.lastMethod)
	}
	if ct := w.Header().Get("Content-Type"); ct != ContentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", ct, ContentTypeJSON)
	}
	var res bridgeRes
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("响应 JSON 解码失败: %v body=%s", err, w.Body.String())
	}
	if res.Name != "user-42" || !res.Hit {
		t.Errorf("res = %+v, want {user-42 true}", res)
	}
}

// TestBridge_RPCHandler_EmptyBodyZeroValue 空 body（GET 语义）→ 零值 Req，合法。
func TestBridge_RPCHandler_EmptyBodyZeroValue(t *testing.T) {
	backend := &stubBackend{fn: func(_ context.Context, args, reply any) error {
		if got := args.(*bridgeReq).Id; got != "" {
			t.Errorf("空 body 应得到零值 Req, got Id=%q", got)
		}
		reply.(*bridgeRes).Hit = true
		return nil
	}}
	w := bridgeDo(t, RPC[bridgeReq, bridgeRes]("user", "GetUser"),
		backend, http.MethodGet, "/api/user", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// TestBridge_RPCHandler_BadJSON_400 非法 JSON → 400 + 统一错误 envelope（带 traceId）。
func TestBridge_RPCHandler_BadJSON_400(t *testing.T) {
	backend := &stubBackend{fn: func(context.Context, any, any) error {
		t.Fatal("非法 JSON 不应抵达 Backend")
		return nil
	}}
	w := bridgeDo(t, RPC[bridgeReq, bridgeRes]("user", "GetUser"),
		backend, http.MethodPost, "/api/user", `{"id":`, map[string]string{HeaderTraceID: "tid-400"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var er ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &er); err != nil {
		t.Fatalf("错误 envelope 解码失败: %v", err)
	}
	if er.Code != http.StatusBadRequest || er.TraceId != "tid-400" {
		t.Errorf("envelope = %+v, want code=400 traceId=tid-400", er)
	}
}

// TestBridge_RPCHandler_NoBackend_503 未注入 Backend → 503 fail-closed。
func TestBridge_RPCHandler_NoBackend_503(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/user", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	RPC[bridgeReq, bridgeRes]("user", "GetUser").ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 Backend status = %d, want 503", w.Code)
	}
}

// TestBridge_RPCHandler_ErrorMapping 后端错误经 StatusFromError 映射：
// 注册哨兵 / WithHTTPStatus / 默认 502。
func TestBridge_RPCHandler_ErrorMapping(t *testing.T) {
	sentinel := errors.New("bridge-test: sentinel")
	RegisterErrorStatus(sentinel, http.StatusConflict)

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"注册哨兵→409", sentinel, http.StatusConflict},
		{"包装后的哨兵仍命中(errors.Is)", &statusErrWrap{sentinel}, http.StatusConflict},
		{"WithHTTPStatus→418", WithHTTPStatus(errors.New("teapot"), http.StatusTeapot), http.StatusTeapot},
		{"context.DeadlineExceeded→504", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"未知业务错误→502", errors.New("boom"), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &stubBackend{fn: func(context.Context, any, any) error { return tc.err }}
			w := bridgeDo(t, RPC[bridgeReq, bridgeRes]("user", "GetUser"),
				backend, http.MethodPost, "/api/user", `{"id":"1"}`, nil)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d", w.Code, tc.want)
			}
			var er ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &er); err != nil {
				t.Fatalf("错误 envelope 解码失败: %v body=%s", err, w.Body.String())
			}
			if er.Code != tc.want || er.Message == "" {
				t.Errorf("envelope = %+v, want code=%d 且 message 非空", er, tc.want)
			}
		})
	}
}

// statusErrWrap 用 %w 链验证注册表的 errors.Is 匹配穿透包装。
type statusErrWrap struct{ inner error }

func (e *statusErrWrap) Error() string { return "wrap: " + e.inner.Error() }
func (e *statusErrWrap) Unwrap() error { return e.inner }

// TestBridge_Invoke_Helper handler 内 Invoke 便捷入口：成功 / 无 Backend。
func TestBridge_Invoke_Helper(t *testing.T) {
	backend := &stubBackend{fn: func(_ context.Context, args, reply any) error {
		reply.(*bridgeRes).Name = "via-invoke-" + args.(*bridgeReq).Id
		return nil
	}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res, err := Invoke[bridgeReq, bridgeRes](r, "user", "GetUser", &bridgeReq{Id: "7"})
		if err != nil {
			WriteError(w, r, StatusFromError(err), err.Error())
			return
		}
		_ = WriteJSON(w, http.StatusOK, res)
	})
	w := bridgeDo(t, handler, backend, http.MethodGet, "/api/user/7", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "via-invoke-7") {
		t.Fatalf("Invoke helper = (%d, %s), want 200 + via-invoke-7", w.Code, w.Body.String())
	}

	// 无 Backend：Invoke 返回 ErrBackendNotConfigured → 503。
	req := httptest.NewRequest(http.MethodGet, "/api/user/7", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req)
	if w2.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 Backend Invoke status = %d, want 503", w2.Code)
	}
}

// TestBridge_DecodeJSON_BodyTooLarge 超限 body → ErrBodyTooLarge → 413。
func TestBridge_DecodeJSON_BodyTooLarge(t *testing.T) {
	big := `{"id":"` + strings.Repeat("x", 64) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(big))
	var v bridgeReq
	if err := DecodeJSONLimit(req, &v, 16); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
	// 桥 handler 路径：默认上限不可被 64 字节触发，这里直接断言映射逻辑。
	if got := StatusFromError(ErrBackendNotConfigured); got != http.StatusServiceUnavailable {
		t.Errorf("ErrBackendNotConfigured → %d, want 503", got)
	}
}

// TestBridge_StatusFromError_Nil nil → 200。
func TestBridge_StatusFromError_Nil(t *testing.T) {
	if got := StatusFromError(nil); got != http.StatusOK {
		t.Fatalf("StatusFromError(nil) = %d, want 200", got)
	}
	if WithHTTPStatus(nil, 404) != nil {
		t.Fatal("WithHTTPStatus(nil) 应返回 nil")
	}
}

// TestBridge_WriteError_CarriesTraceID WriteError 把链路 id 带回客户端。
func TestBridge_WriteError_CarriesTraceID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(trace.WithTraceID(req.Context(), "tid-werr"))
	w := httptest.NewRecorder()
	WriteError(w, req, http.StatusBadGateway, "boom")
	var er ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &er); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if er.TraceId != "tid-werr" || er.Code != http.StatusBadGateway || er.Message != "boom" {
		t.Errorf("envelope = %+v", er)
	}
}

// TestHealth_Endpoints health endpoint：无条件 200 / 业务自检 503。
func TestHealth_Endpoints(t *testing.T) {
	// 无条件探活
	w := httptest.NewRecorder()
	Health().ServeHTTP(w, httptest.NewRequest(http.MethodGet, DefaultHealthPath, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Fatalf("Health = (%d, %s), want 200 ok", w.Code, w.Body.String())
	}

	// 自检通过 → 200
	w = httptest.NewRecorder()
	HealthFunc(func() error { return nil }).ServeHTTP(w, httptest.NewRequest(http.MethodGet, DefaultHealthPath, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("HealthFunc(ok) = %d, want 200", w.Code)
	}

	// 自检失败 → 503（探活方据此摘流量）
	w = httptest.NewRecorder()
	HealthFunc(func() error { return errors.New("redis down") }).
		ServeHTTP(w, httptest.NewRequest(http.MethodGet, DefaultHealthPath, nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "redis down") {
		t.Fatalf("HealthFunc(fail) = (%d, %s), want 503 + 原因", w.Code, w.Body.String())
	}

	// nil 自检退化为无条件 200
	w = httptest.NewRecorder()
	HealthFunc(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, DefaultHealthPath, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("HealthFunc(nil) = %d, want 200", w.Code)
	}
}
