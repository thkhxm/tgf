package rpc

import (
	"context"
	"crypto/subtle"
	"github.com/rs/cors"
	"github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/exp/admin"
	"github.com/thkhxm/tgf/log"
	"net/http"
	"os"
	"strings"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/2/20
//***************************************************

// AdminTokenEnv 是 admin 控制面运维口令的环境变量名。
// 该口令与 D7 玩家登录鉴权完全解耦——这里是独立的运维口令，
// 仅用于保护 admin HTTP 控制面（服务上下线等运维操作）。
const AdminTokenEnv = "ADMIN_TOKEN"

// adminAuthHeader 是携带运维口令的 HTTP 请求头（Authorization: Bearer <token>）。
const adminAuthHeader = "Authorization"

// adminBearerPrefix 是 Authorization 头中 Bearer 方案的前缀。
const adminBearerPrefix = "Bearer "

// adminTokenHeader 是携带运维口令的备用 HTTP 请求头，便于运维工具直接传 token。
const adminTokenHeader = "X-Admin-Token"

// readAdminToken 从环境变量直接读取运维口令。
// 之所以走 os.Getenv 而非 tgf.GetStrConfig 快照，是因为运维口令属于敏感凭据，
// 不应进入框架配置 mapping（会被日志/导出路径意外打印），而是按需即时从进程环境读取。
func readAdminToken() string {
	return strings.TrimSpace(os.Getenv(AdminTokenEnv))
}

// extractRequestToken 从请求头中提取调用方携带的运维口令，
// 优先读取 Authorization: Bearer <token>，回退到 X-Admin-Token。
func extractRequestToken(r *http.Request) string {
	if v := r.Header.Get(adminAuthHeader); v != "" {
		if strings.HasPrefix(v, adminBearerPrefix) {
			return strings.TrimSpace(v[len(adminBearerPrefix):])
		}
		// 兼容调用方直接把裸 token 放进 Authorization 头的情况
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(r.Header.Get(adminTokenHeader))
}

// AuthMiddleware 为 admin 控制类操作（服务上下线等）加运维口令鉴权。
// 行为（fail-closed，默认拒绝）：
//   - 若未配置 ADMIN_TOKEN（环境变量为空）：拒绝所有受保护操作，返回 503 并记 ErrorTag，
//     从而避免"未配置口令 = 控制面裸奔"这一最差组合。
//   - 若配置了 ADMIN_TOKEN：要求请求携带且匹配口令，否则返回 401。
//     口令比对使用 subtle.ConstantTimeCompare 防时序侧信道。
//
// 该中间件与 CorsMiddleware 平级，专门负责鉴权这一层职责。
func AuthMiddleware(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 放行 CORS 预检：浏览器预检不带自定义鉴权头，且不触发任何控制类副作用。
		if r.Method == http.MethodOptions {
			handler.ServeHTTP(w, r)
			return
		}
		token := readAdminToken()
		if token == "" {
			log.ErrorTag("admin", "ADMIN_TOKEN 未配置，拒绝控制面请求 path=%s remote=%s（请配置环境变量 %s 后再访问 admin 控制面）",
				r.URL.Path, r.RemoteAddr, AdminTokenEnv)
			http.Error(w, "admin control plane disabled: ADMIN_TOKEN not configured", http.StatusServiceUnavailable)
			return
		}
		reqToken := extractRequestToken(r)
		if reqToken == "" || subtle.ConstantTimeCompare([]byte(reqToken), []byte(token)) != 1 {
			log.WarnTag("admin", "admin 控制面鉴权失败 path=%s remote=%s", r.URL.Path, r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}
}

func ServeAdmin(port string) (r <-chan bool) {
	mux := http.NewServeMux()
	c := &admin.ConsulRegistry{}
	c.InitRegistry()
	c.StateCallBack = func(name, address string, state client.ConsulServerState) {
		s := state
		SendNoReplyRPCMessageByAddress(name, address, "StateHandler", &s)
	}
	// 控制类操作（active/close/pause 可远程上下线服务）必须经 AuthMiddleware 鉴权；
	// 只读列表/监控查询同样纳入鉴权，避免管理面信息泄露。
	mux.HandleFunc("/consul", CorsMiddleware(AuthMiddleware(c.ConsulList)))
	mux.HandleFunc("/consul/active/{id}", CorsMiddleware(AuthMiddleware(c.ActivateService)))
	mux.HandleFunc("/consul/close/{id}", CorsMiddleware(AuthMiddleware(c.DeactivateService)))
	mux.HandleFunc("/consul/pause/{id}", CorsMiddleware(AuthMiddleware(c.PauseService)))
	//
	mux.HandleFunc("/monitor/service", CorsMiddleware(AuthMiddleware(admin.QueryMonitor)))
	r = NewRPCServer().WithService(&Admin{Module: Module{Name: tgf.AdminServiceModuleName, Version: "1.0"}}).WithCache(tgf.CacheModuleClose).Run()
	go func() {
		corsVar := cors.New(cors.Options{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{
				http.MethodPost,
				http.MethodGet,
				http.MethodOptions,
			},
			AllowedHeaders:   []string{"*"},
			AllowCredentials: true,
		})
		handler := corsVar.Handler(mux)
		// 启动日志必须在阻塞的 ListenAndServe 之前打印，否则正常情况下永远执行不到。
		log.InfoTag("admin", "admin server start at port %s", port)
		// ListenAndServe 的 error 必须记录，不能静默吞掉——
		// 否则端口冲突等启动失败时管理面静默不可用，运维无从排查。
		if err := http.ListenAndServe(":"+port, handler); err != nil {
			log.ErrorTag("admin", "admin server 启动/运行失败 port=%s err=%v", port, err)
		}
	}()
	return
}

// CorsMiddleware 当前为透传占位（真正的 CORS 头由 rs/cors 包在 ServeAdmin 中统一处理）。
// 保留该函数以兼容既有装配链；鉴权职责由 AuthMiddleware 独立承担。
func CorsMiddleware(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}
}

type Admin struct {
	Module
	autoUpdateTicker *time.Ticker
}

func (a *Admin) L(ctx context.Context, args *string, reply *string) (err error) {
	return
}

func (a *Admin) Destroy(sub IService) {
}

func (a *Admin) GetName() string {
	return a.Name
}

func (a *Admin) GetVersion() string {
	return a.Version
}

func (a *Admin) Startup() (bool, error) {
	var ()
	a.autoUpdateTicker = time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-a.autoUpdateTicker.C:
				a.autoUpdateMonitor()
			}
		}
	}()
	return true, nil
}

func (a *Admin) autoUpdateMonitor() {
	var (
		rc = getRPCClient()
		//xclient = rc.getClient(api.ModuleName)
	)
	ctx := NewRPCContext()
	all := admin.NodeMonitorData{}
	rc.clients.Range(func(s string, xClient client.XClient) bool {
		r := &admin.NodeMonitorData{}
		arg := ""
		xClient.Call(ctx, "ASyncMonitor", &arg, r)
		for _, datum := range r.Data {
			d1 := false
			for _, data := range all.Data {
				if data.Group == datum.Group {
					d1 = true
					for _, value := range datum.Values {
						d2 := false
						for _, item := range data.Values {
							if item.Key == value.Key {
								item.Count += value.Count
								d2 = true
								break
							}
						}
						if !d2 {
							data.Values = append(data.Values, value)
						}
					}
				}
			}
			if !d1 {
				all.Data = append(all.Data, datum)
			}
		}
		return true
	})
	admin.AddSecondMonitor(all)
}
