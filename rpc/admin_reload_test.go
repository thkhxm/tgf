package rpc

// E 档配置热更触发入口（admin POST /config/reload）的接线测试：
//   1. 仅 POST 可触发（GET → 405）；
//   2. 经 AuthMiddleware 鉴权（无 token → 401/503）；
//   3. Reload 端到端生效：改 env → POST → tgfconfig.Current() 拿到新值，
//      且 rpc 包注册的 OnReload 订阅者（默认 RPC 超时）真实热更——
//      这是"可热更项"契约（启动读 Current + OnReload 原子替换运行态）的接线证明。

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tgfconfig "github.com/thkhxm/tgf/config"
)

// doConfigReloadRequest 用 AuthMiddleware 包装 handleConfigReload 发一次请求。
func doConfigReloadRequest(method, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/config/reload", nil)
	if token != "" {
		req.Header.Set(adminAuthHeader, adminBearerPrefix+token)
	}
	rec := httptest.NewRecorder()
	AuthMiddleware(handleConfigReload)(rec, req)
	return rec
}

func TestHandleConfigReload_MethodNotAllowed(t *testing.T) {
	const token = "reload-ops-token"
	setEnvConfigForTest(t, AdminTokenEnv, token)

	rec := doConfigReloadRequest(http.MethodGet, token)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /config/reload status = %d, want 405", rec.Code)
	}
}

func TestHandleConfigReload_RequiresAuth(t *testing.T) {
	const token = "reload-ops-token"
	setEnvConfigForTest(t, AdminTokenEnv, token)

	rec := doConfigReloadRequest(http.MethodPost, "wrong-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token status = %d, want 401", rec.Code)
	}
}

// TestHandleConfigReload_AppliesNewConfigAndHotUpdatesTimeout 是热更契约的
// 端到端接线证明：
//   改 env RPCDefaultTimeoutMs=1234 → POST /config/reload（带正确 token）→
//   1) tgfconfig.Current().RPC.DefaultTimeoutMs == 1234（快照原子替换）
//   2) resolveRPCTimeout 默认值 == 1234ms（rpc 包 OnReload 订阅者把新值
//      原子写入 defaultRPCTimeoutNanos——运行态真实热更，不是只换了快照）
func TestHandleConfigReload_AppliesNewConfigAndHotUpdatesTimeout(t *testing.T) {
	const token = "reload-ops-token"
	// AuthMiddleware 读配置快照，token 需先进快照；RPCDefaultTimeoutMs 的新值
	// 也一并写入 env（setEnvConfigForTest 的预 Reload 会吃进去，但下面会先把
	// 运行态改成错值，由被测端点触发的 Reload 重新覆盖——证明端点接线生效）。
	setEnvConfigForTest(t, AdminTokenEnv, token)
	setEnvConfigForTest(t, "RPCDefaultTimeoutMs", "1234")
	// 把运行态超时改成错值，验证端点触发的 OnReload 订阅者会重新覆盖它。
	SetDefaultRPCTimeout(9 * time.Second)

	rec := doConfigReloadRequest(http.MethodPost, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /config/reload status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}

	if got := tgfconfig.Current().RPC.DefaultTimeoutMs; got != 1234 {
		t.Errorf("Current().RPC.DefaultTimeoutMs = %d, want 1234", got)
	}
	if got := resolveRPCTimeout("", ""); got != 1234*time.Millisecond {
		t.Errorf("resolveRPCTimeout 默认值 = %v, want 1234ms（OnReload 订阅者应热更运行态）", got)
	}
}
