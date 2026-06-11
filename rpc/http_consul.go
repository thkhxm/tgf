package rpc

import (
	"net"
	"strings"
	"time"

	"github.com/thkhxm/tgf/v2"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/rpc/internal"
	"github.com/thkhxm/tgf/v2/util"
	"github.com/thkhxm/tgf/v2/web"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description G2/G3：HTTP 服务注册进 Consul（rpc 侧编排）——
//
// WithHTTPServiceConsul = WithHTTPService + "把这个 HTTP 服务注册进 Consul
// （带 health endpoint）"，使分布式 web 服务可被标准 Consul 生态（DNS / API /
// fabio / traefik）发现与负载均衡。实现复用 F2 的健康检查形态（默认 TTL
// 续约，可选 Consul 主动 HTTP 探测），注册体构造在 internal/http_service.go。
//
// 生命周期接线（与 D3 优雅停机统一编排）：
//   - Run()：startHTTPServers 之后 registerHTTPConsulServices——注册发生在
//     HTTP 监听就绪之后（与 F2 "Serve-ready-before-register" 同一时序原则），
//     TTL 模式随注册启动续约 goroutine；
//   - Destroy()：unregisterFromConsul（rpcx 节点摘除）之后立即
//     deregisterHTTPConsulServices——先摘流量再 drain（shutdownHTTPServers
//     在其后执行），新请求不再路由过来，in-flight 正常完成。
//
//2026/6/10
//***************************************************

// HTTPRegistration 是 WithHTTPServiceConsul 的 Consul 注册参数。
// 零值字段按生产默认解析（见各字段注释）。
type HTTPRegistration struct {
	// ServiceName Consul service 逻辑名（发现/负载均衡的检索键）。必填，
	// 为空时该 HTTP 服务照常启动但跳过 Consul 注册（启动期 Error 日志提示）。
	ServiceName string

	// Address 显式指定对外可达地址 "host:port"。空 → host 取配置
	// ServiceAddress（WithCustomServiceAddress 时）或本机出口 IP，port 取
	// HTTP 实际监听端口（":0" 随机端口也能注册到真实值）。
	Address string

	// HealthPath health endpoint 路径。空 → web.DefaultHealthPath("/health")。
	// 除非 DisableHealthRoute，路由会被自动挂载（web.Health / web.HealthFunc）。
	HealthPath string

	// HealthCheck 业务自检函数（如 Redis/MySQL ping）。非 nil 时自动挂载的
	// health 路由用 web.HealthFunc(HealthCheck)（自检失败返回 503 摘流量），
	// nil 时用 web.Health()（端口活性级探活）。
	HealthCheck func() error

	// DisableHealthRoute 已自行注册 health 路由（路径冲突会 panic——ServeMux
	// 语义）或不需要 endpoint 时置 true。
	DisableHealthRoute bool

	// UseHTTPCheck true → Consul agent 主动 GET health endpoint（要求 agent
	// 网络可达本服务）；false（默认）→ TTL check，框架续约 goroutine 维持
	//（容器/NAT 环境零配置可用，语义与 F2 节点心跳一致）。
	UseHTTPCheck bool

	// Interval TTL 续约周期 / HTTP 探测间隔。<=0 → internal.DefaultTTLHealthInterval(5s)。
	Interval time.Duration
	// Timeout HTTP check 模式的单次探测超时。<=0 → 2s。TTL 模式忽略。
	Timeout time.Duration
	// DeregisterCriticalAfter critical 后自动摘除时长。<1m → Consul 硬下限 1m。
	DeregisterCriticalAfter time.Duration

	// Tags / Meta 透传到 Consul service。
	Tags []string
	Meta map[string]string
}

// httpConsulHandle 是已注册 HTTP service 的最小消费接口。
// 生产实现是 internal.ConsulHTTPService；接口化是为了单测注入桩验证
// "Run 注册 → 续约 goroutine → Destroy 反注册"的完整接线（与 F2
// ttlHealthRenewer 同手法）。
type httpConsulHandle interface {
	// NeedsRenew TTL 模式返回 true（需要框架侧续约 goroutine）。
	NeedsRenew() bool
	// Renew 续约 TTL check（续约 goroutine 每 tick 调用）。
	Renew(note string) error
	// Interval 续约/探测周期（续约 goroutine 对齐 ticker）。
	Interval() time.Duration
	// Deregister 从 Consul 摘除该 service（优雅停机调用）。
	Deregister() error
	// ServiceID 注册的 service ID（日志/断言）。
	ServiceID() string
}

// newHTTPConsulServiceFn 是 internal.NewConsulHTTPService 的注入点（单测替换
// 为桩）。包装函数显式归一错误路径返回值为无类型 nil，避免 typed-nil 进接口。
var newHTTPConsulServiceFn = func(opt internal.HTTPServiceOptions) (httpConsulHandle, error) {
	h, err := internal.NewConsulHTTPService(opt)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// WithHTTPServiceConsul 装载一个 HTTP 服务（语义同 WithHTTPService）并把它
// 注册进 Consul（带 health endpoint）。可多次调用（不同端口/不同 ServiceName）；
// 与 WithHTTPService 可混用（后者不注册 Consul）。
//
// 自动行为：
//   - 除非 reg.DisableHealthRoute，在路由表追加 "GET <HealthPath>"（默认
//     /health）——reg.HealthCheck 非 nil 时带业务自检（失败 503 摘流量）；
//   - Run 时以 HTTP 实际监听地址注册（":0" 随机端口注册真实端口）；
//   - Destroy 时反注册（在停 accept/drain 之前，先摘流量）。
//
// 典型用法（client-only 纯 web 进程，分布式可发现）：
//
//	rpc.NewRPCServer().
//	    WithClientOnly().
//	    WithHTTPServiceConsul(web.Options{
//	        Routes: func(r *web.Router) {
//	            r.POST("/api/user", web.RPC[GetUserReq, GetUserRes]("user", "GetUser"))
//	        },
//	    }, rpc.HTTPRegistration{ServiceName: "user-api"}).
//	    Run()
func (s *Server) WithHTTPServiceConsul(opt web.Options, reg HTTPRegistration) *Server {
	if !reg.DisableHealthRoute {
		userRoutes := opt.Routes
		healthPath := normalizeHealthPath(reg.HealthPath)
		check := reg.HealthCheck
		opt.Routes = func(r *web.Router) {
			if userRoutes != nil {
				userRoutes(r)
			}
			r.Handle("GET "+healthPath, web.HealthFunc(check))
		}
	}
	s.WithHTTPService(opt)
	if s.httpConsulRegs == nil {
		s.httpConsulRegs = make(map[int]*HTTPRegistration)
	}
	regCopy := reg
	s.httpConsulRegs[len(s.httpOptions)-1] = &regCopy
	log.InfoTag("init", "HTTP 服务将注册进 Consul service=%v healthPath=%v httpCheck=%v",
		reg.ServiceName, normalizeHealthPath(reg.HealthPath), reg.UseHTTPCheck)
	return s
}

// normalizeHealthPath 归一 health 路径：空 → web.DefaultHealthPath，补前导 /。
func normalizeHealthPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return web.DefaultHealthPath
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// registerHTTPConsulServices 把 WithHTTPServiceConsul 声明的 HTTP 服务注册进
// Consul。由 Run / runClientOnly 在 startHTTPServers 之后调用——此刻监听已
// 就绪，注册即可服务（F2 时序原则）。
//
// 失败语义与 F2 setupConsulTTLHealth 一致：单个注册失败只告警不阻断启动
// （HTTP 服务照常监听，只是不可被发现）——Consul 抖动不应放大为发布失败。
func (s *Server) registerHTTPConsulServices() {
	if len(s.httpConsulRegs) == 0 {
		return
	}
	if s.disableConsul {
		log.WarnTag("init", "WithoutConsul 已开启,跳过 %d 个 HTTP 服务的 Consul 注册", len(s.httpConsulRegs))
		return
	}
	for idx := 0; idx < len(s.httpServers); idx++ {
		reg, ok := s.httpConsulRegs[idx]
		if !ok {
			continue
		}
		if strings.TrimSpace(reg.ServiceName) == "" {
			log.Error("[init] HTTP 服务 Consul 注册跳过: ServiceName 为空 addr=%v", s.httpServers[idx].Addr())
			continue
		}
		advertise := s.resolveHTTPAdvertiseAddr(reg.Address, s.httpServers[idx].Addr())
		mode := internal.HTTPCheckModeTTL
		if reg.UseHTTPCheck {
			mode = internal.HTTPCheckModeHTTP
		}
		h, err := newHTTPConsulServiceFn(internal.HTTPServiceOptions{
			ServiceName:             reg.ServiceName,
			Address:                 advertise,
			HealthPath:              normalizeHealthPath(reg.HealthPath),
			CheckMode:               mode,
			Interval:                reg.Interval,
			Timeout:                 reg.Timeout,
			DeregisterCriticalAfter: reg.DeregisterCriticalAfter,
			Tags:                    reg.Tags,
			Meta:                    reg.Meta,
		})
		if err != nil {
			log.WarnTag("init", "HTTP 服务 Consul 注册失败(服务照常监听,暂不可被发现) service=%v addr=%v err=%v",
				reg.ServiceName, advertise, err)
			continue
		}
		s.httpConsulServices = append(s.httpConsulServices, h)
		if h.NeedsRenew() {
			s.startHTTPConsulRenewLoop(h)
		}
		log.InfoTag("init", "HTTP 服务已注册进 Consul service=%v addr=%v check=%v", h.ServiceID(), advertise, mode)
	}
}

// resolveHTTPAdvertiseAddr 解析对外公告地址：显式 Address 优先；否则用 HTTP
// 真实监听地址，通配 host（"" / 0.0.0.0 / ::）替换为配置 ServiceAddress
// （WithCustomServiceAddress 时）或本机出口 IP——与 rpcx 节点注册同源策略。
func (s *Server) resolveHTTPAdvertiseAddr(explicit, listen string) string {
	if explicit != "" {
		return explicit
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		if s.customServiceAddress {
			host = tgfconfig.Current().Service.Address
		} else {
			host = util.GetLocalHost()
		}
	}
	return net.JoinHostPort(host, port)
}

// startHTTPConsulRenewLoop 为一个 TTL 模式的注册启动续约 goroutine。
// 退出信号 httpConsulRenewStop 全实例共享，Destroy 时统一关闭。
// 续约失败只告警（Renew 内部已含 check 丢失自愈重注册，下个 tick 自然重试；
// 持续失败时 Consul 在 TTL 内把该 HTTP service 置 critical 并自动摘除——
// 这正是"框架死了 Consul 必须知道"的供给侧语义）。
func (s *Server) startHTTPConsulRenewLoop(h httpConsulHandle) {
	if s.httpConsulRenewStop == nil {
		s.httpConsulRenewStop = make(chan struct{})
	}
	stop := s.httpConsulRenewStop
	util.Go(func() {
		ticker := time.NewTicker(h.Interval())
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := h.Renew("tgf http service heartbeat nodeId=" + tgf.NodeId); err != nil {
					log.WarnTag("health", "Consul HTTP service TTL 续约失败(下个 tick 重试) service=%v err=%v",
						h.ServiceID(), err)
				}
			case <-stop:
				log.InfoTag("health", "Consul HTTP service 续约停止 service=%v", h.ServiceID())
				return
			}
		}
	})
}

// deregisterHTTPConsulServices 是 D3 停机序列的 HTTP 摘流量步骤：停续约
// goroutine 并从 Consul 摘除全部 HTTP service。在 shutdownHTTPServers（drain）
// 之前执行——新请求不再被 LB 路由过来，in-flight 由 drain 正常送完。
// 超时兜底与 unregisterFromConsul 一致：Consul 不可达时不阻塞停机
// （TTL/DeregisterCriticalServiceAfter 兜底摘除）。
func (s *Server) deregisterHTTPConsulServices() {
	if s.httpConsulRenewStop != nil {
		close(s.httpConsulRenewStop)
		s.httpConsulRenewStop = nil
	}
	if len(s.httpConsulServices) == 0 {
		return
	}
	handles := s.httpConsulServices
	done := make(chan struct{})
	util.Go(func() {
		defer close(done)
		for _, h := range handles {
			if err := h.Deregister(); err != nil {
				log.WarnTag("shutdown", "Consul HTTP service 反注册失败(将随 critical 自动摘除兜底) service=%v err=%v",
					h.ServiceID(), err)
				continue
			}
			log.InfoTag("shutdown", "Consul HTTP service 已摘除 service=%v", h.ServiceID())
		}
	})
	select {
	case <-done:
	case <-time.After(consulUnregisterTimeout):
		log.WarnTag("shutdown", "Consul HTTP service 反注册超时(%v),继续停机(将随 TTL/critical 过期摘除)", consulUnregisterTimeout)
	}
}
