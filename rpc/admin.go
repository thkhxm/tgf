package rpc

import (
	"context"
	"github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/exp/admin"
	"net/http"
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

func ServeAdmin() {
	mux := http.NewServeMux()
	c := &admin.ConsulRegistry{}
	c.InitRegistry()
	c.StateCallBack = func(name, address string, state client.ConsulServerState) {
		s := state
		SendNoReplyRPCMessageByAddress(name, address, "StateHandler", &s)
	}
	mux.HandleFunc("/consul", CorsMiddleware(c.ConsulList))
	mux.HandleFunc("/consul/active/{id}", CorsMiddleware(c.ActivateService))
	mux.HandleFunc("/consul/close/{id}", CorsMiddleware(c.DeactivateService))
	mux.HandleFunc("/consul/pause/{id}", CorsMiddleware(c.PauseService))
	//
	mux.HandleFunc("/monitor/service", CorsMiddleware(admin.QueryMonitor))
	r := NewRPCServer().WithService(&Admin{Module: Module{Name: tgf.AdminServiceModuleName, Version: "1.0"}}).WithCache(tgf.CacheModuleClose).Run()
	go func() {
		<-r
	}()
	go http.ListenAndServe(":8080", mux)
}

func CorsMiddleware(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")                                                                                         // 指明哪些请求源被允许访问资源，值可以为 "*"，"null"，或者单个源地址。
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,AccessToken,X-CSRF-Token, Authorization, Token")                              //对于预请求来说，指明了哪些头信息可以用于实际的请求中。
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")                                                                       //对于预请求来说，哪些请求方式可以用于实际的请求。
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Access-Control-Allow-Origin, Access-Control-Allow-Headers, Content-Type") //对于预请求来说，指明哪些头信息可以安全的暴露给 CORS API 规范的 API
		w.Header().Set("Access-Control-Allow-Credentials", "true")                                                                                 //指明当请求中省略 creadentials 标识时响应是否暴露。对于预请求来说，它表明实际的请求中可以包含用户凭证。

		//放行所有OPTIONS方法
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
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
func (a *Admin) GetUserHook() IUserHook {
	return nil
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
