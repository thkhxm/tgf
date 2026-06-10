package rpc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description admin 控制面运维口令鉴权单测（D6）
//2026/6/10
//***************************************************

// okHandler 是被保护的下游 handler，被命中即写 200，用于断言"放行"。
func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// TestAuthMiddleware 表驱动覆盖 AuthMiddleware 的全部分支：
//   - 未配置 ADMIN_TOKEN → 503（fail-closed，控制面禁用）
//   - 配置了 ADMIN_TOKEN 但请求不带/带错 token → 401
//   - 配置了 ADMIN_TOKEN 且请求带正确 token（Bearer / 裸值 / X-Admin-Token）→ 200
//   - OPTIONS 预检放行 → 200
func TestAuthMiddleware(t *testing.T) {
	const goodToken = "s3cr3t-ops-token"

	tests := []struct {
		name       string
		envToken   string // 通过 t.Setenv 注入的 ADMIN_TOKEN（空串表示不配置）
		method     string
		authHeader string // Authorization 头取值（空表示不设置）
		tokenHdr   string // X-Admin-Token 头取值（空表示不设置）
		wantStatus int
	}{
		{
			name:       "未配置口令_拒绝所有控制操作_503",
			envToken:   "",
			method:     http.MethodPost,
			authHeader: "Bearer " + goodToken,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "已配置口令_请求不带token_401",
			envToken:   goodToken,
			method:     http.MethodPost,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "已配置口令_token错误_401",
			envToken:   goodToken,
			method:     http.MethodPost,
			authHeader: "Bearer wrong-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "已配置口令_Bearer正确token_200",
			envToken:   goodToken,
			method:     http.MethodPost,
			authHeader: "Bearer " + goodToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "已配置口令_Authorization裸token正确_200",
			envToken:   goodToken,
			method:     http.MethodPost,
			authHeader: goodToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "已配置口令_X-Admin-Token正确_200",
			envToken:   goodToken,
			method:     http.MethodGet,
			tokenHdr:   goodToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "已配置口令_X-Admin-Token错误_401",
			envToken:   goodToken,
			method:     http.MethodGet,
			tokenHdr:   "nope",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "OPTIONS预检放行_200",
			envToken:   goodToken,
			method:     http.MethodOptions,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv 自动在子测试结束时还原，且禁止与 t.Parallel 并用，天然隔离环境变量。
			if tt.envToken != "" {
				t.Setenv(AdminTokenEnv, tt.envToken)
			} else {
				// 显式清空，避免被外部环境/其它用例污染。
				t.Setenv(AdminTokenEnv, "")
			}

			req := httptest.NewRequest(tt.method, "/consul/close/svc-1", nil)
			if tt.authHeader != "" {
				req.Header.Set(adminAuthHeader, tt.authHeader)
			}
			if tt.tokenHdr != "" {
				req.Header.Set(adminTokenHeader, tt.tokenHdr)
			}
			rec := httptest.NewRecorder()

			AuthMiddleware(okHandler)(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestExtractRequestToken 验证从请求头提取 token 的优先级与裁剪逻辑。
func TestExtractRequestToken(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		tokenHdr   string
		want       string
	}{
		{name: "Bearer前缀", authHeader: "Bearer abc", want: "abc"},
		{name: "Bearer前缀带多余空格", authHeader: "Bearer   abc  ", want: "abc"},
		{name: "Authorization裸token", authHeader: "abc", want: "abc"},
		{name: "回退X-Admin-Token", tokenHdr: "xyz", want: "xyz"},
		{name: "Authorization优先于X-Admin-Token", authHeader: "Bearer a", tokenHdr: "b", want: "a"},
		{name: "两头皆空", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/consul", nil)
			if tt.authHeader != "" {
				req.Header.Set(adminAuthHeader, tt.authHeader)
			}
			if tt.tokenHdr != "" {
				req.Header.Set(adminTokenHeader, tt.tokenHdr)
			}
			if got := extractRequestToken(req); got != tt.want {
				t.Fatalf("extractRequestToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReadAdminToken 验证运维口令读取会裁剪首尾空白。
func TestReadAdminToken(t *testing.T) {
	t.Setenv(AdminTokenEnv, "  padded-token  ")
	if got := readAdminToken(); got != "padded-token" {
		t.Fatalf("readAdminToken() = %q, want %q", got, "padded-token")
	}

	t.Setenv(AdminTokenEnv, "   ")
	if got := readAdminToken(); got != "" {
		t.Fatalf("readAdminToken() with blank env = %q, want empty", got)
	}
}
