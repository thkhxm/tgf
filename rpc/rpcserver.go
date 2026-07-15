package rpc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cornelk/hashmap"
	client2 "github.com/thkhxm/rpcx-consul/v2/client"
	"github.com/thkhxm/rpcx/v2/client"
	"github.com/thkhxm/rpcx/v2/server"
	"github.com/thkhxm/rpcx/v2/share"
	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/component"
	tgfconfig "github.com/thkhxm/tgf/v2/config"
	"github.com/thkhxm/tgf/v2/db"
	"github.com/thkhxm/tgf/v2/log"
	"github.com/thkhxm/tgf/v2/metrics"
	"github.com/thkhxm/tgf/v2/platform"
	"github.com/thkhxm/tgf/v2/rpc/internal"
	"github.com/thkhxm/tgf/v2/trace"
	"github.com/thkhxm/tgf/v2/util"
	"github.com/thkhxm/tgf/v2/web"
	"google.golang.org/protobuf/proto"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

const (
	defaultMaxWorkers  = 1e4
	defaultMaxCapacity = 1e4
)

var singletonLock = &sync.Mutex{}

// Server
// @Description:
//
// A4 说明：Hook 装载顺序明确为两阶段：
//
//	preServe 阶段（= 原 beforeOptionals，Run 里 rpcx Serve 启动前执行）
//	├─ 1. 默认 Consul 发现装载      （除非 WithoutConsul()）
//	└─ 2. 用户通过 With* 注册的 Hook（WithServerPool / WithGateway / WithGameConfig / ...）
//
//	postServe 阶段（= 原 afterOptionals，Run 里 rpcx Serve goroutine 启动后执行）
//	├─ 1. 用户注册的 Hook
//	└─ 2. 默认 RPC Client 启动      （除非 WithoutServiceClient()）
//
// 顺序策略的理由：
//   - Consul 装载必须在用户 Hook 之前，因为某些 Hook（未来的 WithStandalone 等）
//     可能会查 `internal.GetDiscovery()` 做开关判断。
//   - RPC Client 启动必须在 rpcx Serve 之后，因为它要 watch 自己的注册条目。
type Server struct {
	rpcServer *server.Server

	// beforeOptionals 是"Serve 前执行"的用户 Hook 列表。字段名保留为
	// beforeOptionals 避免内部大量改名，语义等价于新的 preServe 阶段。
	beforeOptionals []Optional
	// afterOptionals 是"Serve 后执行"的用户 Hook 列表。语义等价于新的 postServe 阶段。
	afterOptionals []Optional

	maxWorkers  int
	maxCapacity int
	//
	service []IService
	//
	minPort int32
	maxPort int32

	enableProfile bool

	customServiceAddress bool

	//
	whiteServiceList []string

	// A4 新增：默认模块的开关。构造时全为 false（=默认开启）。
	// WithoutConsul / WithoutServiceClient 设置对应标志。
	disableConsul bool
	disableClient bool

	// 单进程模式新增：开启后 SendRPCMessage 会先查 localDispatcher
	// 做进程内反射调用，命中即绕开 rpcx/Consul。
	// 由 WithInProcessDispatch / WithSingleProcess 设置。
	inProcessDispatch bool

	// A6 新增、F2 接线：后台健康心跳。
	// healthInterval > 0 时 Run 会启动一个心跳 goroutine；Consul 开启且服务注册
	// 成功时（consulHealth 非 nil），每 tick 对 Consul agent TTL check 续约
	// （internal.ConsulTTLHealth.Renew），进程 kill 后 Consul 在 TTL 内自动把
	// 检查置 critical 并按 DeregisterCriticalServiceAfter 摘除节点；
	// 无 Consul 时保持 A6 的"atomic 状态位 + 日志"行为。
	// Consul 开启时未显式 WithHealthCheck 也会以 internal.DefaultTTLHealthInterval
	// 默认开启（见 setupConsulTTLHealth）。
	healthInterval time.Duration
	// healthCheckStop 是心跳 goroutine 的退出信号，Destroy 时关闭。
	healthCheckStop chan struct{}
	// healthy 是进程级健康标志。F2 之后：接了 Consul TTL 时反映"最近一次续约
	// 是否成功"；未接 Consul 时维持 A6 语义（心跳 goroutine 活着即 true）。
	healthy atomic.Bool
	// consulHealth 是 Consul agent TTL health check 句柄（F2）。
	// Run 在服务注册成功后、心跳 goroutine 启动前一次性赋值；心跳循环与
	// Destroy 只读——无并发写。nil = 未启用（无 Consul / 全部注册失败）。
	consulHealth ttlHealthRenewer

	// D3 / P0-3 优雅停机相关：
	// registryPlugin 持有 Consul 注册插件（internal.ConsulDiscovery.RegisterServer
	// 的返回值），Destroy 时调用其 Stop() 完成「KV 反注册 + 停止 TTL 刷新 goroutine」。
	// 只调 rpcx 的 DoUnregister 删 KV 不够：rpcx-consul 的 TTL 刷新 goroutine 发现
	// 节点缺失会自动重建（serverplugin/consul.go 的 "will re-create" 分支），
	// 节点会在 UpdateInterval 内"复活"，导致 Consul 残留已停机节点。
	registryPlugin server.Plugin
	// shutdownDrainTimeout 是 drain in-flight 阶段（rpcServer.Shutdown）的超时，
	// 由 WithShutdownDrainTimeout 配置，零值时用 defaultShutdownDrainTimeout。
	shutdownDrainTimeout time.Duration
	// destroyed 保证 Destroy 只执行一次（信号编排与业务手动调用可能并存）。
	destroyed atomic.Bool

	// G1 HTTP 一等公民：
	// httpOptions 是 WithHTTPService 注册的 HTTP 服务配置（可多实例，如业务
	// API 与内部管理面分端口）；Run 时经 applyHTTPDefaults 填配置默认值并启动。
	httpOptions []web.Options
	// httpServers 是已启动的 HTTP 服务实例，Destroy 时按序优雅停机
	//（http.Server.Shutdown 带超时 drain，见 shutdownHTTPServers）。
	httpServers []*web.Server

	// G3 client-only（rpcserver_clientonly.go）：
	// clientOnly 开启后 Run 分流到 runClientOnly——不注册任何 service、不监听
	// rpcx 端口，仅初始化 RPC client（+ 可选 HTTP 服务）调用后端。
	clientOnly bool

	// G2/G3 HTTP 服务注册进 Consul（http_consul.go）：
	// httpConsulRegs 记录 WithHTTPServiceConsul 的注册意图，key 与 httpOptions
	// 下标对齐（混用 WithHTTPService 时未注册的下标缺位即可）。
	httpConsulRegs map[int]*HTTPRegistration
	// httpConsulServices 是已注册进 Consul 的 HTTP service 句柄，Destroy 反注册。
	httpConsulServices []httpConsulHandle
	// httpConsulRenewStop 是 TTL 续约 goroutine 的共享退出信号，Destroy 关闭。
	httpConsulRenewStop chan struct{}
}

type loginHook func(ctx context.Context, userId string) (err error)
type offlineHook func(ctx context.Context, userId string, replace bool) (err error)

type Optional func(*Server)

// WithoutConsul 关闭默认的 Consul 服务发现装载。
// A4 新增：用于单机 / 单测 / 未来单进程调试模式，避免强制依赖外部 Consul。
// 默认情况下（不调用此方法）Consul 发现仍然被自动装上。
func (s *Server) WithoutConsul() *Server {
	s.disableConsul = true
	return s
}

// WithoutServiceClient 关闭默认的 RPC Client 启动（包括 discovery watch）。
// A4 新增：单机模式下也不需要 client 自动 watch Consul。
func (s *Server) WithoutServiceClient() *Server {
	s.disableClient = true
	return s
}

// WithStandalone 是 `WithoutConsul().WithoutServiceClient()` 的语义糖。
// C1 新增：用于单机 / 本地开发 / 单元测试场景——不依赖 Consul 也不启动 RPC
// client watch。典型用法：
//
//	rpc.NewRPCServer().
//	    WithStandalone().
//	    WithService(myService).
//	    Run()
//
// 内部只是同时置上两个 disable 标志位，不做额外配置。
func (s *Server) WithStandalone() *Server {
	return s.WithoutConsul().WithoutServiceClient()
}

// WithHealthCheck 配置健康心跳的续约周期。传 0 或负数视为不改动。
//
// F2 之后的语义（A6 骨架已接真实 Consul TTL check）：
//   - Consul 开启（默认）：心跳每 interval 对 Consul agent TTL check 续约一次，
//     TTL = 3×interval——进程 kill / 宕机后 Consul 在 TTL 内把检查置 critical
//     并按 DeregisterCriticalServiceAfter（1m，Consul 硬下限）自动摘除节点。
//     不调本方法时 Run 也会以 internal.DefaultTTLHealthInterval(5s) 默认开启。
//   - WithoutConsul / 单进程模式：维持 A6 行为——atomic 状态位 + Debug 日志，
//     且不调本方法时心跳不启动。
//
// interval 建议 2~10 秒：太短增加 Consul agent 压力，太长拉高死节点的发现延迟。
func (s *Server) WithHealthCheck(interval time.Duration) *Server {
	if interval > 0 {
		s.healthInterval = interval
	}
	return s
}

// IsHealthy 返回当前进程的健康状态快照。
// 未开 WithHealthCheck 时恒为 false；开了之后从第一次心跳 tick 开始为 true，
// Destroy 时 goroutine 退出后会被重新置为 false。
func (s *Server) IsHealthy() bool {
	return s.healthy.Load()
}

// startHealthCheckLoop 在 Run 里启动心跳 goroutine。幂等——多次调用只启动一次。
// 内部细节：
//   - healthCheckStop 是退出信号，由 Destroy 关闭
//   - goroutine 启动后立即把 healthy 置 true（表示进程 bootstrapping 完成）
//   - F2：consulHealth 非 nil 时每 tick 对 Consul agent TTL check 续约——
//     这是"节点宕机后 Consul 在 TTL 内自动摘除"的供给侧（续约停止 = 节点死亡信号）。
//     consulHealth 在 Run 内、本 goroutine 启动前一次性赋值，循环内只读，无竞态。
func (s *Server) startHealthCheckLoop() {
	if s.healthInterval <= 0 {
		return
	}
	if s.healthCheckStop != nil {
		return // 已启动
	}
	s.healthCheckStop = make(chan struct{})
	stop := s.healthCheckStop
	interval := s.healthInterval
	health := s.consulHealth
	s.healthy.Store(true)

	util.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if health == nil {
					// 未接 Consul（WithoutConsul/单进程/注册失败）：维持 A6 行为。
					log.DebugTag("health", "heartbeat tick nodeId=%v healthy=%v",
						tgf.NodeId, s.healthy.Load())
					continue
				}
				// F2：真实 Consul TTL 续约。失败置 unhealthy 并在下个 tick 重试
				// （Renew 内部已含"check 丢失自愈重注册"逻辑）。
				if err := health.Renew(fmt.Sprintf("nodeId=%v", tgf.NodeId)); err != nil {
					s.healthy.Store(false)
					log.WarnTag("health", "Consul TTL 续约失败(下个 tick 重试) nodeId=%v err=%v",
						tgf.NodeId, err)
					continue
				}
				if !s.healthy.Swap(true) {
					log.InfoTag("health", "Consul TTL 续约恢复 nodeId=%v", tgf.NodeId)
				}
			case <-stop:
				s.healthy.Store(false)
				log.InfoTag("health", "heartbeat loop stopped nodeId=%v", tgf.NodeId)
				return
			}
		}
	})
}

// stopHealthCheckLoop 由 Destroy 调用。幂等。
func (s *Server) stopHealthCheckLoop() {
	if s.healthCheckStop == nil {
		return
	}
	close(s.healthCheckStop)
	s.healthCheckStop = nil
}

// ttlHealthRenewer 是 Consul agent TTL health check 的最小消费接口（F2）。
// 生产实现是 internal.ConsulTTLHealth；接口化是为了单测可注入桩验证
// "Run 注册成功后挂载 → 心跳续约 → Destroy 反注册"的完整接线。
type ttlHealthRenewer interface {
	// Renew 续约 TTL check（心跳 goroutine 每 tick 调用）。
	Renew(note string) error
	// Deregister 从 Consul agent 摘除本节点 service（优雅停机调用）。
	Deregister() error
}

// newTTLHealthFn 是 internal.NewConsulTTLHealth 的注入点（单测替换为桩）。
// 包装函数显式归一错误路径的返回值为无类型 nil，避免 typed-nil 进接口。
var newTTLHealthFn = func(opt internal.TTLHealthOptions) (ttlHealthRenewer, error) {
	h, err := internal.NewConsulTTLHealth(opt)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// setupConsulTTLHealth 注册 Consul agent TTL health check（F2，Run 的第 5 步）。
// 失败不阻断启动：KV 注册已成功（自带 session TTL 兜底），agent check 是
// 健康可观测层——记 Warn 后节点照常服务。
// 未显式 WithHealthCheck 时以 internal.DefaultTTLHealthInterval 默认开启心跳，
// 保证"Consul 开启 → 死节点必然在 TTL 内被发现"不依赖业务记得调 Option。
func (s *Server) setupConsulTTLHealth(ip string, modules []string) {
	if s.healthInterval <= 0 {
		s.healthInterval = internal.DefaultTTLHealthInterval
	}
	h, err := newTTLHealthFn(internal.TTLHealthOptions{
		ServiceAddress: ip,
		Modules:        modules,
		Interval:       s.healthInterval,
	})
	if err != nil {
		log.WarnTag("init", "Consul TTL health check 注册失败(节点仍可服务,agent 健康检查不可用) err=%v", err)
		return
	}
	s.consulHealth = h
	log.InfoTag("init", "Consul TTL health check 已挂载 addr=%v interval=%v modules=%v",
		ip, s.healthInterval, modules)
}

func (s *Server) WithServerPool(maxWorkers, maxCapacity int) *Server {
	s.beforeOptionals = append(s.beforeOptionals, func(server *Server) {
		server.maxWorkers = maxWorkers
		server.maxCapacity = maxCapacity
		log.InfoTag("init", "修改rpcx协程池大小 maxWorkers=%v maxCapacity=%v", maxWorkers, maxCapacity)
	})
	return s
}

func (s *Server) WithService(service IService) *Server {
	s.service = append(s.service, service)
	log.InfoTag("init", "装载逻辑服务[%v@%v]", service.GetName(), service.GetVersion())
	return s
}

func (s *Server) WithRandomServicePort(minPort, maxPort int32) *Server {
	var ()
	s.minPort = minPort
	s.maxPort = maxPort
	return s
}

func (s *Server) WithCache(module tgf.CacheModule) *Server {
	var ()
	db.WithCacheModule(module)
	return s
}

func (s *Server) WithWhiteService(serviceName string) *Server {
	var ()
	s.whiteServiceList = append(s.whiteServiceList, serviceName)
	return s
}

func (s *Server) WithGameConfig(path string) *Server {
	s.beforeOptionals = append(s.beforeOptionals, func(server *Server) {
		component.WithConfPath(path)
		component.InitGameConfToMem()
		log.InfoTag("init", "装载游戏配置,读取[%v]路径下的json文件", path)
	})
	return s
}

// WithCustomServiceAddress
// @Description: 开启自定义地址注册，通过常量ServiceAddress注册绑定的ip
// @receiver this
func (s *Server) WithCustomServiceAddress() {
	s.customServiceAddress = true
}

// buildPreServeHooks 组装 rpcx Serve 之前要运行的 Hook 列表。
// A4 把"默认 Consul 装载"从 NewRPCServer 的硬编码改为这里按 disableConsul 条件注入。
// 拆成独立方法便于单测断言"按标志生成的 pipeline 形状"。
func (s *Server) buildPreServeHooks() []Optional {
	hooks := make([]Optional, 0, len(s.beforeOptionals)+1)
	if !s.disableConsul {
		// 默认 Consul 发现放在用户 Hook 之前，保证后续 Hook 可以读 internal.GetDiscovery()。
		hooks = append(hooks, func(*Server) {
			internal.UseConsulDiscovery()
		})
	}
	hooks = append(hooks, s.beforeOptionals...)
	return hooks
}

// buildPostServeHooks 组装 rpcx Serve goroutine 启动后要运行的 Hook 列表。
// 默认 RPC Client watch 放在用户 postServe Hook 之后——A4 让它可以被 WithoutServiceClient 关闭。
func (s *Server) buildPostServeHooks() []Optional {
	hooks := make([]Optional, 0, len(s.afterOptionals)+1)
	hooks = append(hooks, s.afterOptionals...)
	if !s.disableClient {
		hooks = append(hooks, func(sv *Server) {
			c := newRPCClient().startup(sv.whiteServiceList...)
			// D4/D5: startup 在 discovery 缺失或 Consul 不可达时返回 nil——
			// 此时跳过白名单装载，避免 nil 解引用；后续 getRPCClient 会重试。
			if c == nil {
				log.WarnTag("init", "RPCClient 启动失败,白名单暂未装载(将随 client 重试)")
				return
			}
			log.InfoTag("init", "装载RPCClient服务")
			for _, messageType := range sv.whiteServiceList {
				log.InfoTag("init", "加入请求无需登录的白名单 serviceName=%v", messageType)
			}
		})
	}
	return hooks
}

// GatewayOptions 是 C1 引入的统一网关配置入口。
// 它把 v1 的 `WithGateway` / `WithGatewayWS` / `WithGatewayWSS` / `WithGatewayKCP`
// 四个方法合并到单一 builder 点，减少 builder 表面积并消除"能不能同时开 TCP+WS+KCP"
// 的语义歧义（答案是可以，这个 struct 给出清晰的表达方式）。
//
// 零值语义：
//   - `TCPPort == ""` → tcpBuilder 走默认端口（newTCPBuilder() 里定义）
//   - `WSPath == ""`  → 不启动 WebSocket listener
//   - WSTLSKey / WSTLSCert 同时非空 → 启用 WSS（带 TLS 的 WS）
//   - `KCP == nil`    → 不启动 KCP listener
//
// 典型用法：
//
//	rpc.NewRPCServer().
//	    WithGatewayOptions(rpc.GatewayOptions{
//	        TCPPort:   "8082",
//	        WSPath:    "/ws",
//	        KCP:       rpc.NewKCPBuilder("8300").WithAEADKey(key),
//	    }).
//	    Run()
type GatewayOptions struct {
	// TCPPort 是 TCP listener 端口。v1 语义一致。
	TCPPort string
	// WSPath 是 WebSocket 路径。为空时不启动 WS。
	WSPath string
	// WSTLSCert / WSTLSKey 同时非空时启用 WSS（WS over TLS）。
	// 注意 v1 的 WithGatewayWSS 参数顺序是 (port, path, key, cert)，字段名方向是
	// cert 在前 key 在后——这里的命名沿用 builder.WithWss(key, cert) 的方向。
	WSTLSKey  string
	WSTLSCert string
	// KCP 非 nil 时挂 KCP listener 到 GateService，和 TCP/WS 共享生命周期。
	KCP IKCPBuilder
}

// WithGatewayOptions 是 C1 统一的网关装载入口。
// 如果同时调了老的 `WithGateway*` 家族和本方法，会生成多个 GateService，
// 它们竞争 users 表——**不要混用**。新代码统一走 WithGatewayOptions。
func (s *Server) WithGatewayOptions(opt GatewayOptions) *Server {
	s.beforeOptionals = append(s.beforeOptionals, func(server *Server) {
		builder := newTCPBuilder()
		if opt.TCPPort != "" {
			builder.WithPort(opt.TCPPort)
		}
		if opt.WSPath != "" {
			builder.WithWSPath(opt.WSPath)
		}
		if opt.WSTLSKey != "" && opt.WSTLSCert != "" {
			builder.WithWss(opt.WSTLSKey, opt.WSTLSCert)
		}

		var gateway IService
		if opt.KCP != nil {
			gateway = GatewayServiceWithKCP(builder, opt.KCP)
		} else {
			gateway = GatewayService(builder)
		}
		s.service = append(s.service, gateway)
		log.InfoTag("init", "装载逻辑服务[%v@%v] tcp=%v ws=%v kcp=%v",
			gateway.GetName(), gateway.GetVersion(),
			opt.TCPPort != "", opt.WSPath != "", opt.KCP != nil)
	})
	return s
}

// WithGateway 老式单 TCP 网关入口。
//
// Deprecated: 使用 `WithGatewayOptions(GatewayOptions{TCPPort: port})`
// 或 `WithStandalone()` + `WithGatewayOptions(...)`。v2 后续小版本会删。
func (s *Server) WithGateway(port string) *Server {
	return s.WithGatewayOptions(GatewayOptions{TCPPort: port})
}

// WithGatewayWSS 老式 WSS 网关入口。
//
// Deprecated: 使用 WithGatewayOptions。
func (s *Server) WithGatewayWSS(port, path, key, cert string) *Server {
	return s.WithGatewayOptions(GatewayOptions{
		TCPPort:   port,
		WSPath:    path,
		WSTLSKey:  key,
		WSTLSCert: cert,
	})
}

// WithGatewayWS 老式 WS 网关入口。
//
// Deprecated: 使用 WithGatewayOptions。
func (s *Server) WithGatewayWS(port, path string) *Server {
	return s.WithGatewayOptions(GatewayOptions{TCPPort: port, WSPath: path})
}

// WithGatewayKCP 装载 KCP 网关作为 GateService 的副 listener。
//
// A8 决策细节：
//   - KCP 和 TCP 共享同一个 TCPServer 实例（users / handleConn / doLogic 三者共用）
//   - 如果同时 WithGateway(tcpPort) + WithGatewayKCP(kcpPort)，节点会同时监听两种
//     传输——客户端选其一走即可。A3 的 Redis 登录锁保证同一 uid 不能同时登录两次。
//   - 如果只调 WithGatewayKCP 没调 WithGateway，GateService 的 tcpBuilder 会默认
//     用 newTCPBuilder()（TCP 端口 defaultTcpServerPort 仍会监听，因为 TCPServer.Run
//     必须起一个 listener）。推荐显式 WithGateway 搭配使用。
//   - AEAD 密钥必须通过 kcpBuilder.WithAEADKey(key) 显式设置；不设就是明文模式
//     （仅限开发/内网）。
//
// 典型用法：
//
//	key := [32]byte{...}
//	kcp := rpc.NewKCPBuilder("8300").WithAEADKey(key[:])
//	rpc.NewRPCServer().
//	    WithGateway("8082").
//	    WithGatewayKCP(kcp).
//	    WithService(...).
//	    Run()
func (s *Server) WithGatewayKCP(kcpBuilder IKCPBuilder) *Server {
	s.beforeOptionals = append(s.beforeOptionals, func(server *Server) {
		// 找到已有的 GateService 附加 KCP——否则新建一个 GateService with 默认 TCP builder。
		for i, svc := range s.service {
			if gs, ok := svc.(*GateService); ok {
				gs.kcpBuilder = kcpBuilder
				s.service[i] = gs
				log.InfoTag("init", "装载KCP网关到已有的GateService port=%v", kcpBuilder.Port())
				return
			}
		}
		// 没找到 GateService——新建一个 TCP + KCP 组合的 GateService
		tcpBuilder := newTCPBuilder()
		gateway := GatewayServiceWithKCP(tcpBuilder, kcpBuilder)
		s.service = append(s.service, gateway)
		log.InfoTag("init", "装载逻辑服务[%v@%v] 含KCP listener",
			gateway.GetName(), gateway.GetVersion())
	})
	return s
}

func (s *Server) WithProfileDebug() *Server {
	s.enableProfile = true
	return s
}

// ---------------------------------------------------------------------------
// G1：HTTP 一等公民（tgf/web 包的 rpc 侧接线）
// ---------------------------------------------------------------------------
//
// 架构（防 import 环）：web 包自包含（HTTP server / 路由 / 中间件 / 生命周期），
// 不 import rpc；rpc 单向 import web，并把"调用后端 RPC 的能力"以 web.Backend
// 接口注入（见 defaultWebBackend）——HTTP handler 经 web.BackendFromRequest
// 拿到它即可调用任意后端 service，单进程直通（local dispatcher）与分布式
// rpcx 路径自动选择，限流/熔断策略管道（E1）、超时（A7）、metrics（E3）与
// traceId 透传（HTTP X-Trace-Id → rpcx ReqMetaData["TraceId"]）全部生效。
//
// 生命周期（与 D3 优雅停机统一编排）：
//   - Run()：postServe 钩子之后启动 HTTP 监听（startHTTPServers）；监听失败
//     与 rpcx 监听失败同语义——非零码退出；
//   - Destroy()：网关停 accept 之后、rpcx drain 之前，对每个 HTTP 实例执行
//     http.Server.Shutdown（带 ShutdownTimeout）drain in-flight 请求——HTTP
//     handler 可能还要调后端 RPC，所以必须先 drain HTTP 再 drain RPC。
//
// 最小用法（DX 示例）：
//
//	rpc.NewRPCServer().
//	    WithService(&UserService{}).
//	    WithHTTPService(web.Options{
//	        // Addr 省略时读配置 HTTPPort（默认 8090）
//	        Routes: func(r *web.Router) {
//	            r.GET("/users/{id}", func(w http.ResponseWriter, req *http.Request) {
//	                backend, _ := web.BackendFromRequest(req)
//	                id := req.PathValue("id")
//	                var reply UserRes
//	                if err := backend.Invoke(req.Context(), "user", "GetUser", &id, &reply); err != nil {
//	                    http.Error(w, err.Error(), http.StatusBadGateway)
//	                    return
//	                }
//	                // ... 序列化 reply
//	            })
//	            admin := r.Group("/admin", web.Auth(web.StaticBearerToken(token)))
//	            admin.GET("/stats", statsHandler)
//	        },
//	        Limiter: web.NewTokenBucketLimiter(1000),
//	    }).
//	    Run()

// WithHTTPService 装载一个与 RPC 服务同进程、共生命周期的 HTTP 服务（G1）。
// 可多次调用装载多个 HTTP 实例（不同端口）。opt 的零值字段在 Run 时按
// 配置项（HTTPPort / HTTPReadHeaderTimeoutSec / HTTPShutdownTimeoutSec）与
// web 包默认值填充；opt.Backend 为 nil 时自动注入框架默认后端调用实现。
func (s *Server) WithHTTPService(opt web.Options) *Server {
	s.httpOptions = append(s.httpOptions, opt)
	log.InfoTag("init", "装载HTTP服务 addr=%v (空地址将在 Run 时读配置 HTTPPort)", opt.Addr)
	return s
}

// applyHTTPDefaults 用 tgf 配置系统（E2 唯一真源）填充 Options 零值字段。
// 在 Run 时调用（而非 WithHTTPService 时）——读到的是启动时刻最新的配置快照。
func applyHTTPDefaults(opt *web.Options) {
	cfg := tgfconfig.Current().HTTP
	if opt.Addr == "" {
		port := cfg.Port
		if port == "" {
			port = "8090"
		}
		opt.Addr = ":" + port
	}
	if opt.ReadHeaderTimeout <= 0 && cfg.ReadHeaderTimeoutSec > 0 {
		opt.ReadHeaderTimeout = time.Duration(cfg.ReadHeaderTimeoutSec) * time.Second
	}
	if opt.ShutdownTimeout <= 0 && cfg.ShutdownTimeoutSec > 0 {
		opt.ShutdownTimeout = time.Duration(cfg.ShutdownTimeoutSec) * time.Second
	}
}

// startHTTPServers 在 Run 的收尾阶段启动全部 HTTP 服务。
// 监听失败与 rpcx 监听失败同语义：启动期失败必须以非零码退出，
// 不允许"进程活着但 HTTP 端口没起来"的半启动状态。
func (s *Server) startHTTPServers() {
	for i := range s.httpOptions {
		opt := s.httpOptions[i]
		applyHTTPDefaults(&opt)
		if opt.Backend == nil {
			opt.Backend = defaultWebBackend()
		}
		srv := web.NewServer(opt)
		if err := srv.Start(); err != nil {
			log.Error("[init] HTTP 服务启动失败 addr=%v err=%v", opt.Addr, err)
			os.Exit(1)
		}
		s.httpServers = append(s.httpServers, srv)
		log.InfoTag("init", "HTTP 服务启动成功 addr=%v", srv.Addr())
	}
}

// shutdownHTTPServers 是 D3 停机序列中的 HTTP drain 步骤：对每个实例执行
// http.Server.Shutdown（带该实例的 ShutdownTimeout）——停止接收新连接并等待
// in-flight 请求处理完成；超时只告警不阻断后续停机（与 drain RPC 同语义）。
func (s *Server) shutdownHTTPServers() {
	for _, hs := range s.httpServers {
		ctx, cancel := context.WithTimeout(context.Background(), hs.ShutdownTimeout())
		if err := hs.Shutdown(ctx); err != nil {
			log.WarnTag("shutdown", "HTTP 服务 drain 未在 %v 内完成(继续停机) addr=%v err=%v",
				hs.ShutdownTimeout(), hs.Addr(), err)
		} else {
			log.InfoTag("shutdown", "HTTP 服务已优雅停机 addr=%v", hs.Addr())
		}
		cancel()
	}
}

// defaultWebBackend 构造注入给 web 包的默认后端调用实现（G1 注入点，
// BRIDGE/G2 的 HTTP→RPC 编解码桥也基于同一个 web.Backend 契约接线）。
func defaultWebBackend() web.Backend {
	return web.BackendFunc(webBackendInvoke)
}

// webBackendInvoke 是默认 Backend 的执行体。路径选择与网关主链路 sendMessage
// 一致：单进程模式命中 local dispatcher 走进程内反射直通（含 E1 策略管道），
// 否则走 SendRPCMessageByStr（自带策略管道 + A7 超时）。两条路径共用
// E3 的 RPC 时延/调用量/错误率埋点（observeRPCCall）。
func webBackendInvoke(ctx context.Context, module, method string, args, reply any) (err error) {
	startTime := time.Now()
	defer func() {
		observeRPCCall(module, method, startTime, err)
	}()

	rctx := newWebBackendContext(ctx)
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(module); ok {
			release, perr := applyMethodPolicy(module, method)
			if perr != nil {
				err = perr
				return
			}
			defer func() {
				release(err)
			}()
			err = localDispatcher.Call(rctx, module, method, args, reply)
			return
		}
	}
	err = SendRPCMessageByStr(rctx, module, method, args, reply)
	return
}

// newWebBackendContext 把 HTTP 请求 context 升级为 rpcx share.Context：
//   - 标记 RPCTip（与 NewRPCContext 一致）；
//   - 把 HTTP 链路的 traceId（web.Trace 中间件注入，trace 包私有 key）写进
//     ReqMetaData["TraceId"]——经 rpcx 协议透传到后端 service，实现
//     "HTTP 请求 → 后端 RPC" 全链路同一个 traceId（E3 贯通语义）。
//
// 注意：share.NewContext 包装原 ctx，HTTP 请求取消/超时信号对 RPC 路径仍然可见。
func newWebBackendContext(ctx context.Context) context.Context {
	sc := share.NewContext(ctx)
	meta := map[string]string{tgf.ContextKeyRPCType: tgf.RPCTip}
	if tid := trace.TraceIDFromContext(ctx); tid != "" {
		meta[tgf.ContextKeyTRACEID] = tid
	}
	sc.SetValue(share.ReqMetaDataKey, meta)
	return sc
}

// defaultShutdownDrainTimeout 优雅停机中 drain in-flight 阶段的默认超时。
// rpcx Shutdown 的轮询间隔是 1s，所以该值不应小于 1s。
const defaultShutdownDrainTimeout = 10 * time.Second

// WithShutdownDrainTimeout 配置优雅停机中 drain in-flight 阶段
// （等待正在处理的 RPC 请求全部完成）的超时；d <= 0 时忽略保持默认值。
// D3 / P0-3：超时后不再继续等待，停机序列继续推进到终末 flush——
// 宁可放弃个别长尾请求，也不能让脏数据落库被卡死的 drain 拖过看门狗强杀。
func (s *Server) WithShutdownDrainTimeout(d time.Duration) *Server {
	if d > 0 {
		s.shutdownDrainTimeout = d
	}
	return s
}

// WithShutdownTimeout 配置整个优雅停机序列的总看门狗超时
// （tgf.SetShutdownTimeout 的 builder 风格入口，默认 60s）。
// 该值必须大于 WithShutdownDrainTimeout 设置的 drain 超时，
// 否则 drain 尚未完成看门狗就会强制退出。
func (s *Server) WithShutdownTimeout(d time.Duration) *Server {
	tgf.SetShutdownTimeout(d)
	return s
}

// drainTimeout 返回 drain in-flight 阶段的生效超时。
func (s *Server) drainTimeout() time.Duration {
	if s.shutdownDrainTimeout > 0 {
		return s.shutdownDrainTimeout
	}
	return defaultShutdownDrainTimeout
}

// WithMetrics 注入一个 metrics Provider。传 nil 恢复默认 NoOp。
// B4 说明：Provider 是全局单例，这个方法只是 builder 风格的便捷入口，
// 底层等价于 `metrics.SetProvider(p)`。应当在 Run 前一次性完成。
func (s *Server) WithMetrics(p metrics.Provider) *Server {
	metrics.SetProvider(p)
	log.InfoTag("init", "装载 metrics provider=%v", metrics.GetProvider().Name())
	return s
}

// WithTracer 注入一个 trace Tracer。传 nil 恢复默认 NoOp。
// B4 说明：同 WithMetrics，底层等价于 `trace.SetTracer(t)`。
func (s *Server) WithTracer(t trace.Tracer) *Server {
	trace.SetTracer(t)
	log.InfoTag("init", "装载 tracer=%v", trace.GetTracer().Name())
	return s
}

// ---------------------------------------------------------------------------
// H1：第三方平台合约层（tgf/platform 包的 rpc 侧接线）
// ---------------------------------------------------------------------------

// platformExitFn 是 WithPlatform 注册失败 fail-fast 的退出注入点
// （生产恒为 os.Exit；单测替换以断言退出码——与 shutdownFlushFn 同一注入模式）。
var platformExitFn = os.Exit

// WithPlatform 注册一个第三方平台 Provider 到 platform 全局注册表（H1）。
//
// 行为：
//   - 立即注册（与 WithMetrics 同为即时生效型选项，不进 beforeOptionals 队列）——
//     业务在 Run 之前即可经 platform.Login / Payment / Audit / Webhook 取用；
//   - platform.Register 注册时自动套 metrics 包装（成功/失败/时延），业务无感；
//   - 多次调用注册多平台；
//   - 重名 / nil / 空 Name 等注册失败 → 启动报错并以非零码退出（fail-fast，
//     与 rpcx 监听失败、client-only 配置冲突同语义：不允许“进程活着但平台
//     注册表形态不对”的半启动状态）。
//
// 典型用法（平台实现是独立 module github.com/thkhxm/tgf-platform/*，
// 凭据从 config.Current().Platform 读取，详见 doc/platform-sdk-design.md）：
//
//	rpc.NewRPCServer().
//	    WithPlatform(wechat.New(wechat.Config{ /* AppID/Secret 取自 config.Current().Platform */ })).
//	    WithPlatform(tiktok.New(tiktok.Config{ /* ... */ })).
//	    Run()
//
// 任意处按平台名 + 能力取用：
//
//	lp, ok := platform.Login("wechat")   // (platform.LoginProvider, bool)
//	pp, ok := platform.Payment("tiktok") // (platform.PaymentProvider, bool)
func (s *Server) WithPlatform(p platform.Provider) *Server {
	if err := platform.Register(p); err != nil {
		log.Error("[init] 平台注册失败(fail-fast) err=%v", err)
		platformExitFn(1)
		return s // 仅单测注入不退出的 exitFn 时可达
	}
	log.InfoTag("init", "装载平台 provider=%v", p.Name())
	return s
}

func (s *Server) Run() <-chan bool {
	var (
		serviceName    string
		ip             string
		_logServiceMsg string
	)

	//运行数据初始化
	db.Run()

	//开启服务器模式
	tgf.ServerModule = true
	// A4: 两阶段 Hook 调度，顺序由 buildPreServeHooks / buildPostServeHooks 明确定义。
	// 默认 Consul 装载 prepend 到最前，用户 Hook 按注册顺序追加；
	// 这替代了原先 NewRPCServer 里硬编码 withConsulDiscovery + withServiceClient 的写法。
	for _, hook := range s.buildPreServeHooks() {
		hook(s)
	}

	// G3（BRIDGE）：client-only 模式分流——不创建 rpcx server、不监听、不注册
	// service，仅初始化 RPC client + HTTP 服务（见 rpcserver_clientonly.go）。
	// 放在 preServe 钩子之后：Consul discovery 已装载、经钩子追加的 service
	//（WithGatewayOptions 等）已就位，可做完整的配置冲突校验。
	if s.clientOnly {
		return s.runClientOnly()
	}
	/**启动逻辑链*/
	//注册rpcx服务
	op := make([]server.OptionFn, 0)
	for _, ss := range s.service {
		// C2: GetLogicSyncMethod 重命名为 LogicSyncMethods
		methods := ss.LogicSyncMethods()
		if methods == nil {
			continue
		}
		for _, lm := range methods {
			op = append(op, server.WithLogicSync(lm))
		}
	}
	op = append(op, server.WithPool(s.maxWorkers, s.maxCapacity))
	s.rpcServer = server.NewServer(op...)
	s.rpcServer.EnableProfile = s.enableProfile
	// E 档配置读点迁移：启动期一次性项统一走新配置系统的类型化字段
	// （tgf.InitConfig 在 tgf 包 init 已执行，Current() 保证非零值）。
	port := tgfconfig.Current().Service.Port
	if s.minPort > 0 && s.maxPort > s.minPort {
		port = fmt.Sprintf("%v", rand.Int31n(s.maxPort-s.minPort)+s.minPort)
	}
	//rpcx加入服务发现组件
	local := util.GetLocalHost()
	if s.customServiceAddress {
		local = tgfconfig.Current().Service.Address
	}
	ip = fmt.Sprintf("%v:%v", local, port)
	if s.rpcServer.EnableProfile {
		log.InfoTag("init", "开启性能监控:%s", ip+"/debug/statsview")
		log.InfoTag("init", "开启性能监控:%s", ip+"/debug/pprof")
	}

	discovery := internal.GetDiscovery()

	// v2 修复：Startup 与 discovery 注册解耦。
	// v1 行为是 "discovery 非 nil 时才调 Startup"——A4 引入 WithoutConsul
	// 之后，discovery 为 nil 会导致所有 service 的 Startup 被跳过。
	// 单进程模式（WithSingleProcess / WithStandalone）明确需要 Startup 被调用，
	// 所以这里把 Startup 从"注册 discovery"的 if 块里拆出来独立执行。
	//
	// F2 注册时序修正（v3 审计8 P1：原时序"先注册 Consul → 后 Startup 验证 →
	// 最后才 Serve 监听"，且 Startup 失败不反注册——节点在 Consul 可见的瞬间
	// 监听还没起、Startup 失败的服务还残留在注册表，滚动发布期间流量必然打到
	// 未就绪/已死节点）。新时序：
	//   1. 全部 service 先 Startup（失败者剔除出注册名单，绝不对外发布——
	//      比"先注册再反注册"少一个对外可见的脏窗口）；
	//   2. 同步创建 rpcx 监听（net.Listen 返回即内核开始 accept 排队，
	//      这是确定性的"Serve 就绪"信号）；
	//   3. Serve goroutine 接管 accept；
	//   4. 最后 RegisterName——rpcx 先写本地 serviceMap（带锁）再触发
	//      ConsulRegisterPlugin.Register 写 KV，节点在 Consul 可见时必然已可服务；
	//   5. 注册成功后挂 Consul agent TTL health check（心跳 goroutine 续约，
	//      进程 kill 后 Consul 在 TTL 内置 critical 并自动摘除）。
	if discovery != nil {
		// D3：持有 Consul 注册插件引用，优雅停机时调用其 Stop() 完成
		// 「KV 反注册 + 停止 TTL 刷新 goroutine」（见 Destroy / unregisterFromConsul）。
		// F2：插件必须在 Serve 之前装入 pluginContainer（rpcx 的 plugins 切片
		// 无锁，Serve 后 Add 会与连接 accept 回调并发构成 data race）；
		// "装入"≠"注册"——KV 节点发布要等下方步骤 4 的 RegisterName 触发。
		s.registryPlugin = discovery.RegisterServer(ip)
		s.rpcServer.Plugins.Add(s.registryPlugin)
		s.rpcServer.Plugins.Add(NewRPCXServerHandler())
		s.service = append(s.service, &MonitorService{})
	}

	// 1. Startup 先行：全部 service 验证启动，失败者剔除出注册名单。
	// Startup 的两个返回值都是成功契约的一部分：只有 (true, nil)
	// 才算启动成功。否则不得进入后续共用的 dispatcher、能力盘点、
	// rpcx/discovery 注册与 TTL health 列表。
	startedServices := make([]IService, 0, len(s.service))
	for _, service := range s.service {
		serviceName = fmt.Sprintf("%v", service.GetName())
		if startupOK, startupErr := service.Startup(); !startupOK || startupErr != nil {
			log.Error("[init] 服务启动异常,不注册到服务发现 serviceName=%v error=%v", serviceName, startupErr)
			continue
		}
		startedServices = append(startedServices, service)
	}

	// 单进程模式：Startup 完成后把 service 注册到本地 dispatcher。
	// 放在 Startup 之后是因为 Startup 可能修改 service 内部状态（比如 rpc.Module 的 State 字段）。
	s.registerLocalServices(startedServices)
	// D4: 单进程模式下白名单不再依赖 rpcClient（它不启动）——把 WithWhiteService
	// 注册的白名单同步给网关本地直通路径（gateway_local_dispatch.go）。
	if s.inProcessDispatch {
		setLocalGateWhiteList(s.whiteServiceList)
	}

	// 2. 同步创建监听。tgf 的 rpc server 不启用 TLS，net.Listen 等价于 rpcx
	//    tcpMakeListener 的无 TLS 分支；返回后内核已可接受连接。
	ln, lnErr := net.Listen("tcp", ip)
	if lnErr != nil {
		log.Error("[init] rpcx监听创建失败 addr=%v err=%v", ip, lnErr)
		// D3：启动/监听失败必须以非零码退出（原实现 os.Exit(0) 会让
		// 容器编排/发布系统把启动失败误判为正常退出）。
		os.Exit(1)
	}

	// 3. Serve goroutine 接管 accept（ServeListener 与 Serve("tcp", ip) 等价，
	//    仅监听创建被上移到同步路径）。
	util.Go(func() {
		if err := s.rpcServer.ServeListener("tcp", ln); err != nil {
			// D3 / P0-3：优雅停机时 rpcServer.Shutdown 会关闭 listener，
			// Serve 返回 ErrServerClosed——这是停机的正常路径，绝不能在这里
			// os.Exit 把仍在执行的停机序列（drain/终末 flush）直接杀死。
			if errors.Is(err, server.ErrServerClosed) {
				log.InfoTag("shutdown", "rpcx listener 已关闭(优雅停机) addr=%v", ip)
				return
			}
			log.Error("[init] rpcx服务运行异常 addr=%v err=%v", ip, err)
			os.Exit(1)
			return
		}
	})

	// 4. Serve 就绪后注册：RegisterName 先写本地 serviceMap（带锁，Serve 后调用
	//    安全）再触发 Consul KV 发布——发布瞬间本节点已经在 accept。
	registeredModules := make([]string, 0, len(startedServices))
	for _, service := range startedServices {
		serviceName = fmt.Sprintf("%v", service.GetName())
		metaData := fmt.Sprintf("version=%s&nodeId=%s", service.GetVersion(), tgf.NodeId)
		if discovery != nil {
			if err := s.rpcServer.RegisterName(serviceName, service, metaData); err != nil {
				log.Error("[init] 注册服务发现失败 serviceName=%v metaDat=%v error=%v", serviceName, metaData, err)
				continue
			}
			registeredModules = append(registeredModules, serviceName)
			log.InfoTag("init", "注册服务发现 serviceName=%v metaDat=%v", serviceName, metaData)
		} else {
			log.InfoTag("init", "服务启动 serviceName=%v metaDat=%v (无 discovery)", serviceName, metaData)
		}
		_logServiceMsg += serviceName + " " + metaData + ","
	}

	// 5. F2：真实 Consul agent TTL health check（A6 心跳骨架的接线）。
	//    只有至少一个服务注册成功才有挂健康检查的意义；Startup 全失败 →
	//    无注册 → 无 agent service 残留。
	if discovery != nil && len(registeredModules) > 0 {
		s.setupConsulTTLHealth(ip, registeredModules)
	}

	// A4: 走 buildPostServeHooks——用户 Hook 先跑，之后默认 RPC Client watch（除非被关闭）。
	for _, hook := range s.buildPostServeHooks() {
		hook(s)
	}

	// G1: postServe 钩子之后启动 HTTP 服务——分布式模式下默认 Backend 依赖的
	// rpc client watch 已在上面装载，单进程模式下 local dispatcher 也已注册完毕，
	// HTTP 首个请求即可调通后端。监听失败非零码退出（startHTTPServers 内部处理）。
	s.startHTTPServers()

	// G2/G3（BRIDGE）：把 WithHTTPServiceConsul 声明的 HTTP 服务注册进 Consul
	//（带 health endpoint）——监听已就绪，注册即可服务（F2 时序原则）。
	s.registerHTTPConsulServices()

	// A6: 如果 WithHealthCheck 已配置，在 postServe Hook 全部跑完后启动心跳 goroutine。
	// 放在最后是因为心跳的存在条件是"rpcx Serve 已启动 + 默认 client watch 已装载"。
	s.startHealthCheckLoop()

	//启用服务,使用tcp
	log.InfoTag("init", "rpcx服务启动成功 addr=%v service=[%v] ", _logServiceMsg, ip)
	return tgf.CloseChan()
}

// consulUnregisterTimeout Consul 反注册阶段的硬超时（var 而非 const 是为了单测注入）。
// ConsulRegisterPlugin.Stop() 内部是同步网络 KV 操作，Consul 不可达时可能长时间阻塞——
// 停机路径绝不能卡死在摘流量这一步，超时后直接继续后续 drain/flush
// （此时节点 KV 带 TTL，最迟 UpdateInterval+Expired 后自然过期，不会永久残留）。
var consulUnregisterTimeout = 5 * time.Second

// shutdownFlushFn 是 Destroy 第 5 步（终末 flush）的注入点。
// 生产恒为 flushAllOnShutdown；单测替换以断言调用顺序。
var shutdownFlushFn = flushAllOnShutdown

// gateAcceptStopper 是网关停机钩子的最小接口。内置 GateService 实现了它；
// 业务自定义的网关型 service 只要暴露同签名方法，停机时同样会被编排调用。
type gateAcceptStopper interface {
	StopAccept() error
}

// Destroy 优雅停机编排（D3 / P0-3）。由 tgf 的信号处理（SIGTERM/SIGINT）经
// IDestroyHandler 触发，也可由业务手动调用（幂等，只执行一次）。
//
// 完整序列（顺序敏感，不能调换）：
//  1. 停健康心跳；
//  2. 从 Consul 反注册本节点并停止 TTL 刷新 goroutine——新流量不再路由过来，
//     且节点不会被 TTL 刷新"复活"（带超时兜底，Consul 不可达时不阻塞停机）；
//     同阶段反注册 HTTP service（G2/G3，deregisterHTTPConsulServices）——
//     LB/发现侧先摘流量，再进入后续 drain；
//  3. 网关停止 accept 并关闭 TCP/WS/KCP listener（已建立的 TCP/WS 连接不受影响，
//     KCP 例外：kcp-go 会话与 listener 共享 UDP socket，关 listener 会中断既有会话）；
//  4. HTTP 服务优雅停机（G1）——http.Server.Shutdown 带超时 drain in-flight
//     HTTP 请求；必须在 drain RPC 之前：HTTP handler 可能还要调后端 RPC；
//  5. drain in-flight——rpcServer.Shutdown 等待正在处理的 RPC 全部完成（带可配置
//     超时，见 WithShutdownDrainTimeout）；
//  6. 第一轮终末 flush——把 drain 完成时刻的全部脏数据立即落库（db.FlushAll）；
//  7. 业务 service Destroy（每个独立 recover，单个 panic 不中断其余）。
//
// 之后 tgf 的 finalFlushHooks（见本文件 init 注册的桥接）会做第二轮 flush，
// 兜住步骤 6 业务代码新产生的脏数据；整个序列由 tgf 的停机看门狗
// （SetShutdownTimeout / WithShutdownTimeout，默认 60s）兜底防卡死。
func (s *Server) Destroy() {
	if !s.destroyed.CompareAndSwap(false, true) {
		return
	}
	// A6: 先停心跳 goroutine。顺序原因：后续 drain 过程可能需要"进程还活着"的
	// 前提，但摘流量之后心跳已无意义，且业务 Destroy 完成后心跳绝不应再跳。
	s.stopHealthCheckLoop()
	s.unregisterFromConsul()
	// G2/G3（BRIDGE）：HTTP service 的 Consul 反注册与 rpcx 节点摘除同阶段——
	// 必须先于 shutdownHTTPServers（drain）：LB 不再把新请求路由过来，
	// in-flight 由 drain 正常送完。
	s.deregisterHTTPConsulServices()
	s.stopGatewayAccept()
	s.shutdownHTTPServers()
	s.drainInFlight()
	shutdownFlushFn()
	for _, service := range s.service {
		destroyServiceSafely(service)
	}
}

// unregisterFromConsul 完成 Consul 侧的全部反注册（带 consulUnregisterTimeout
// 超时兜底——Consul 不可达时记日志继续停机，不阻塞后续 drain/flush）：
//  1. F2：先摘 agent TTL health service——停机瞬间健康状态即从 Consul 消失，
//     不等 TTL 过期（失败只告警，DeregisterCriticalServiceAfter 兜底）；
//  2. D3：调注册插件的 Stop() 删除本节点全部服务的 KV 注册并停止 TTL 刷新
//     goroutine（节点 KV 随 session TTL 过期兜底）。
func (s *Server) unregisterFromConsul() {
	type stopper interface{ Stop() error }
	sp, spOK := s.registryPlugin.(stopper)
	health := s.consulHealth
	if !spOK && health == nil {
		// 未接 Consul（WithoutConsul / 单进程 / 单测）——无需反注册。
		return
	}
	done := make(chan error, 1)
	util.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("consul 反注册 panic: %v", r)
			}
		}()
		if health != nil {
			if herr := health.Deregister(); herr != nil {
				log.WarnTag("shutdown", "Consul TTL health 反注册失败(将由 critical 自动摘除兜底) nodeId=%v err=%v",
					tgf.NodeId, herr)
			} else {
				log.InfoTag("shutdown", "Consul TTL health 已摘除 nodeId=%v", tgf.NodeId)
			}
		}
		if !spOK {
			done <- nil
			return
		}
		done <- sp.Stop()
	})
	select {
	case err := <-done:
		if err != nil {
			log.WarnTag("shutdown", "Consul 反注册失败(继续停机,节点将随 TTL 过期) nodeId=%v err=%v", tgf.NodeId, err)
			return
		}
		log.InfoTag("shutdown", "Consul 反注册完成,本节点已摘除流量 nodeId=%v", tgf.NodeId)
	case <-time.After(consulUnregisterTimeout):
		log.WarnTag("shutdown", "Consul 反注册超时(%v),继续停机(节点将随 TTL 过期) nodeId=%v", consulUnregisterTimeout, tgf.NodeId)
	}
}

// stopGatewayAccept 调用所有实现了 StopAccept 的 service（内置 GateService 等）
// 的网关停机钩子：停止 accept 并关闭 TCP/WS/KCP listener。单个失败不中断其余。
func (s *Server) stopGatewayAccept() {
	for _, svc := range s.service {
		gs, ok := svc.(gateAcceptStopper)
		if !ok {
			continue
		}
		if err := gs.StopAccept(); err != nil {
			log.WarnTag("shutdown", "网关停止 accept 失败(继续停机) service=%v err=%v", svc.GetName(), err)
			continue
		}
		log.InfoTag("shutdown", "网关已停止接收新连接 service=%v", svc.GetName())
	}
}

// drainInFlight 调用 rpcx Server.Shutdown：从插件反注册（幂等兜底）、关闭 rpcx
// listener、并阻塞等待正在处理的 RPC 请求全部完成；超时（drainTimeout）后放弃等待。
func (s *Server) drainInFlight() {
	if s.rpcServer == nil {
		return
	}
	timeout := s.drainTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := s.rpcServer.Shutdown(ctx); err != nil {
		log.WarnTag("shutdown", "drain in-flight 未在 %v 内全部完成(继续停机) err=%v", timeout, err)
		return
	}
	log.InfoTag("shutdown", "in-flight 请求 drain 完成")
}

// destroyServiceSafely 带 recover 地执行单个 service 的 Destroy——
// 单个业务 Destroy panic 不能中断其余 service 的清理与后续终末 flush。
func destroyServiceSafely(service IService) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorTag("shutdown", "service Destroy panic(已忽略,继续其余清理) service=%v panic=%v", service.GetName(), r)
		}
	}()
	service.Destroy(service)
}

// flushAllOnShutdown 调用 db.FlushAll 把进程内所有 write-behind 管理器的脏数据
// 同步落库，并逐表记录结构化结果。被两处使用：
//  1. Server.Destroy 第 5 步——drain 完成后立即落库（验收要求：SIGTERM 后
//     dirty 数据 100% 落库，不能等业务 Destroy 跑完才 flush）；
//  2. tgf.RegisterFinalFlushHook 注册的终末钩子（init 桥接）——所有
//     IDestroyHandler 之后再兜一轮，覆盖业务 Destroy 期间新产生的脏数据。
//
// db.FlushAll 幂等：无脏数据时为空操作，重复调用安全。
func flushAllOnShutdown() {
	for _, r := range db.FlushAll() {
		if r.Err != nil {
			log.WarnTag("shutdown", "终末 flush 表 %v 部分失败 success=%v failed=%v err=%v (失败条目已尝试写入补偿队列,脏标志保留在内存)",
				r.Table, r.Success, r.Failed, r.Err)
			continue
		}
		log.InfoTag("shutdown", "终末 flush 表 %v 完成 success=%v", r.Table, r.Success)
	}
}

func init() {
	// D3 / P0-3：tgf 根包因循环依赖（db → tgf）不能直接调用 db.FlushAll，
	// 由 rpc 包在此把它桥接为 tgf 的终末 flush 钩子——在所有 IDestroyHandler
	// 跑完之后统一执行（见 tgf/init.go runShutdownSequence）。
	tgf.RegisterFinalFlushHook(flushAllOnShutdown)
}

func NewRPCServer() *Server {
	rpcServer := &Server{}
	rpcServer.afterOptionals = make([]Optional, 0)
	rpcServer.beforeOptionals = make([]Optional, 0)
	rpcServer.maxWorkers = defaultMaxWorkers
	rpcServer.maxCapacity = defaultMaxCapacity

	// A4: 不再在构造时硬编码 withConsulDiscovery / withServiceClient。
	// 这两个默认 Hook 的装载挪到 Run → buildPreServeHooks / buildPostServeHooks，
	// 允许用户在构造之后调 WithoutConsul() / WithoutServiceClient() 关闭默认行为。

	tgf.AddDestroyHandler(rpcServer)
	return rpcServer
}

var rpcClient atomic.Pointer[Client]

func loadRPCClient() *Client {
	return rpcClient.Load()
}

func storeRPCClient(c *Client) {
	rpcClient.Store(c)
}

type Client struct {
	clients     *hashmap.Map[string, client.XClient]
	whiteMethod []string
}

type ClientOptional struct {
}

func newRPCClient() *ClientOptional {
	return &ClientOptional{}
}

// startup
// @Description: 启动rpc客户端
// @receiver this
// D4/D5 加固：
//   - discovery 未初始化（WithoutConsul / 单进程模式）→ 返回 nil，不再对 nil 接口
//     调方法 panic；
//   - RegisterDiscovery 失败（Consul 不可达，D5 后返回 nil 而非缓存 nil）→ 返回 nil
//     且不发布全局 rpcClient，下次 getRPCClient 会重试；
//   - 全局 rpcClient 改为"局部完整构造后最后发布"，消除半初始化窗口。
func (c *ClientOptional) startup(whiteServices ...string) *Client {
	discovery := internal.GetDiscovery()
	if discovery == nil {
		log.WarnTag("init", "discovery 未初始化(WithoutConsul/单进程模式),RPC Client 不启动")
		return nil
	}
	//注册一个basePath的路径
	baseDiscovery := discovery.RegisterDiscovery("")
	if baseDiscovery == nil {
		log.Error("[init] base discovery 创建失败(Consul 不可达?),RPC Client 暂不可用,下次调用将重试")
		return nil
	}

	newClient := new(Client)
	newClient.clients = hashmap.New[string, client.XClient]()
	newClient.whiteMethod = append([]string(nil), whiteServices...)

	//获取当前已经注册了的服务
	for _, v := range baseDiscovery.GetServices() {
		if strings.Index(v.Key, "/") > 0 {
			newClient.registerClient(discovery, strings.Split(v.Key, "/")[0])
		}
	}
	newClient.watchBaseDiscovery(discovery, baseDiscovery)
	// 完整构造后最后发布
	storeRPCClient(newClient)
	return newClient
}

func (c *Client) AddWhiteService(serviceName string) *Client {
	var ()
	c.whiteMethod = append(c.whiteMethod, serviceName)
	return c
}
func (c *Client) CheckWhiteList(serviceName string) bool {
	var ()
	for _, s := range c.whiteMethod {
		if s == serviceName {
			return true
		}
	}
	return false
}

func (c *Client) watchBaseDiscovery(d internal.IRPCDiscovery, discovery *client2.ConsulDiscovery) {
	var ()
	util.Go(func() {
		for kv := range discovery.WatchService() {
			for _, v := range kv {
				if strings.Index(v.Key, "/") > 0 {
					moduleName := strings.Split(v.Key, "/")[0]
					if dis := internal.GetDiscovery().GetDiscovery(moduleName); dis != nil {
						continue
					}
					log.DebugTag("discovery", "base discovery service %v,%v", v.Key, v.Value)
					c.registerClient(d, moduleName)
				}
			}
		}
	})
}

func (c *Client) registerClient(d internal.IRPCDiscovery, moduleName string) (xclient client.XClient) {
	var ()
	discovery := d.RegisterDiscovery(moduleName)
	option := client.DefaultOption

	//if moduleName == tgf.GatewayServiceModuleName {
	//	option.SerializeType = protocol.SerializeNone
	//}
	xclient = client.NewXClient(moduleName, client.Failover, client.SelectByUser, discovery, option)
	//自定义路由
	xclient.SetSelector(NewCustomSelector(moduleName))
	//自定义响应handler
	xclient.GetPlugins().Add(NewRPCXClientHandler())
	c.clients.Set(moduleName, xclient)
	log.InfoTag("init", "注册rpcx client 服务 module=%v ", moduleName)
	return
}

func (c *Client) getClient(moduleName string) (xclient client.XClient) {
	// D4/D5: client 未初始化（单进程模式 / discovery 创建失败）时安全返回 nil，
	// 让所有 Send* API 走"找不到对应模块的服务"错误路径而非 panic。
	if c == nil || c.clients == nil {
		return nil
	}
	if val, ok := c.clients.Get(moduleName); ok {
		xclient = val
	}
	return
}

// getRPCClient 返回全局 RPC client。
// D4/D5: startup 失败（discovery 缺失 / Consul 不可达）时返回 nil——调用方必须
// 判空走错误路径；rpcClient 不会被发布为半成品，后续调用会自动重试初始化。
func getRPCClient() *Client {
	if current := loadRPCClient(); current != nil {
		return current
	}
	singletonLock.Lock()
	defer singletonLock.Unlock()
	if loadRPCClient() == nil {
		if c := newRPCClient().startup(); c != nil {
			log.InfoTag("init", "装载RPCClient服务")
		}
	}
	return loadRPCClient()
}

type Call struct {
	rpcxCall *client.Call
	// release 是策略管道的释放闭包（E1）：异步调用的并发信号量/熔断计数要等
	// 结果出来才能释放，挂在 Done() 上执行。同步路径构造时为 noopRelease。
	release     func(error)
	releaseOnce sync.Once
}

// newCallWithRelease 构造一个携带策略释放闭包的 Call（E1：SendAsyncRPCMessage 用）。
func newCallWithRelease(rpcxCall *client.Call, release func(error)) (call *Call) {
	call = &Call{}
	call.rpcxCall = rpcxCall
	call.release = release
	return
}

// Done
//
//	@Description: 会阻塞
//	@receiver this
//	@return error
func (c *Call) Done() error {
	var ()
	cal := <-c.rpcxCall.Done
	// E1：消费结果时释放策略资源（并发信号量/熔断计数）。Once 保证幂等。
	c.releaseOnce.Do(func() {
		c.release(cal.Error)
	})
	return cal.Error
}

// sendMessage 是网关主链路（tcp.go doLogic → 这里）的统一发送入口——所有客户端
// 请求都从这条路抵达后端 service。
//
// D4 / P0-2 修复：单进程模式（WithSingleProcess / WithStandalone+WithInProcessDispatch）
// 下 discovery 为 nil 且 rpcClient 不启动，原实现首条客户端消息走 getRPCClient()
// → startup() → 对 nil discovery 调方法直接 panic，网关 100% 不可用。
// 现在：本地 dispatcher 命中时走进程内直通（登录态/白名单语义与分布式路径一致），
// 见 gateway_local_dispatch.go。
//
// E1（v3）：本路径接入策略管道（限流/熔断/并发）+ 超时 + metrics——
// 审计指出 C6/A7 只挂在 SendRPCMessage（service 间调用），"客户端→网关→后端"
// 的主流量完全裸奔：无策略、无 deadline 的同步 xclient.Call 会让慢后端无限期
// 阻塞该连接的 logic goroutine（reqChan 容量 16 满后开始丢请求）。
//
// 执行顺序（刻意安排）：
//  1. 登录态/白名单检查——本地拒绝不消耗策略配额、不计熔断（未登录请求风暴
//     不应把后端方法限流配额吃光）；
//  2. applyMethodPolicy（与 SendRPCMessage 完全相同的限流/熔断/并发语义）；
//  3. 本地直通 or 远程 Go+select 超时。
//
// 超时实现说明：连接级 ctx 是 *share.Context，不能用 context.WithTimeout 包装
// （rpcx CustomSelector 等依赖 `ctx.(*share.Context)` 类型断言，包装即断链），
// 所以与 SendRPCMessage 一致采用 xclient.Go + select 模式；超时分支用本地 err
// 而不写 call.Error（避免与 rpcx client 完成路径的写入构成 data race）。
// 本地直通分支保持同步反射调用、不加超时——与 SendRPCMessage 的 local fast path
// 语义一致（单进程模式无网络，超时保护交由 handler 自身负责）。
func sendMessage(ct IUserConnectData, moduleName, serviceName string, args, reply interface{}) (err error) {
	// E3-rpc 埋点：网关主链路与 SendRPCMessage 共用 RPC 时延/调用量/错误率指标。
	startTime := time.Now()
	defer func() {
		observeRPCCall(moduleName, serviceName, startTime, err)
	}()

	messageType := moduleName + "." + serviceName

	useLocal := false
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(moduleName); ok {
			useLocal = true
		}
	}

	// 1. 登录态/白名单（先于策略管道，见函数头注释）
	var (
		rc      *Client
		xclient client.XClient
	)
	if useLocal {
		if !ct.IsLogin() && !checkLocalGateWhiteList(messageType) {
			return fmt.Errorf("用户未登录 非白名单请求无法抵达 moduleName=%v serviceName=%v", moduleName, serviceName)
		}
	} else {
		rc = getRPCClient()
		// D4: rpcClient 不可用（单进程/Standalone 模式、或 discovery 创建失败）时
		// 返回明确错误而非 panic。
		if rc == nil {
			return fmt.Errorf("RPC client 不可用(单进程模式下模块未注册到本地 dispatcher,或 discovery 未初始化) moduleName=%v serviceName=%v", moduleName, serviceName)
		}
		xclient = rc.getClient(moduleName)
		if xclient == nil {
			return fmt.Errorf("找不到对应模块的服务 moduleName=%v serviceName=%v ", moduleName, serviceName)
		}
		if !ct.IsLogin() && !rc.CheckWhiteList(messageType) {
			return fmt.Errorf("用户未登录 非白名单请求无法抵达 moduleName=%v serviceName=%v", moduleName, serviceName)
		}
	}

	// 2. 策略管道（E1：网关主链路与 SendRPCMessage 同语义）
	release, perr := applyMethodPolicy(moduleName, serviceName)
	if perr != nil {
		return perr
	}
	defer func() {
		release(err)
	}()

	// 3a. 单进程直通
	if useLocal {
		return localGateDispatch(ct.GetContextData(), moduleName, serviceName, args, reply)
	}

	// 3b. 远程调用 + 超时（resolveRPCTimeout：per-method 覆盖 → 全局默认）
	done := make(chan *client.Call, 1)
	call, gerr := xclient.Go(ct.GetContextData(), serviceName, args, reply, done)
	if gerr != nil {
		return fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=%w", moduleName, serviceName, gerr)
	}
	if call == nil {
		return fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=无可用服务节点", moduleName, serviceName)
	}
	select {
	case <-time.After(resolveRPCTimeout(moduleName, serviceName)):
		// 不写 call.Error（与 rpcx client 完成路径 race）；in-flight 结果直接放弃。
		return tgf.ErrorRPCTimeOut
	case <-call.Done:
		return call.Error
	}
}

// SendRPCMessage [Req, Res any]
//
//	@Description: 远程rpc调用
//	@param ct
//	@param api
//	@param Res
//	@return res
//	@return err
func SendRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) (res Res, err error) {
	// B4 埋点：进入前记录起始时间戳；任何返回路径 defer 里观测延迟 + 累加计数/错误。
	startTime := time.Now()
	defer func() {
		observeRPCCall(api.ModuleName, api.Name, startTime, err)
	}()

	// C6 策略化：查 per-method 策略，触发限流/熔断/并发控制。
	// 命中快速失败时直接返回对应的 error，不进入 rpcx Go。
	// 注意：策略检查放在 dispatch 分支之前，本地调用和远程调用走完全相同的
	// 限流/熔断/并发语义——对业务代码透明。
	// E1：统一走 applyMethodPolicy 入口（与网关 sendMessage 等全部发送路径共用）。
	policyRelease, perr := applyMethodPolicy(api.ModuleName, api.Name)
	if perr != nil {
		err = perr
		return
	}
	defer func() {
		policyRelease(err)
	}()

	// 单进程模式 fast path：如果开启了 in-process dispatch 且 module 在本地
	// 注册，直接反射调用。绕开 rpcx + Consul 的全部网络路径。
	//
	// 兼容业务通过 New/NewEmpty 或旧代码构造出 typed nil reply 的情况：
	// 在调 Call 之前分配真正的 reply 对象，再用 type assertion 转回 Res。
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(api.ModuleName); ok {
			replyObj := allocateIfNilPtr(api.reply)
			if derr := localDispatcher.Call(ct, api.ModuleName, api.Name, api.args, replyObj); derr != nil {
				err = derr
				return
			}
			if typed, ok := replyObj.(Res); ok {
				return typed, nil
			}
			return api.reply, nil
		}
	}

	var (
		done    = make(chan *client.Call, 1)
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)

	if xclient == nil {
		err = fmt.Errorf("找不到对应模块的服务 moduleName=%v serviceName=%v", api.ModuleName, api.Name)
		log.WarnTag("tcp", "%s", err.Error())
		return
	}
	call, err := xclient.Go(ct, api.Name, api.args, api.reply, done)
	// D5 / P1 修复：xclient.Go 在 selector 选不到节点 / client 已 shutdown 等场景
	// 返回 (nil, err)（如 ErrXClientNoServer）——这是滚动发布期间的常态路径。
	// 原实现把"打印 call.Error"的 defer 注册在 err 检查之前且无条件解引用 call，
	// call==nil 时 return 触发 defer 即 nil 指针 panic，杀死调用方 goroutine。
	// 现在：先检查 err / call==nil 并返回干净 error，defer 挪到检查之后。
	if err != nil {
		// E1：用 %w 保留错误链——熔断错误分类（isCircuitFailure）与上层
		// errors.Is/As 都依赖未被 %v 摊平的原始错误。
		err = fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=%w", api.ModuleName, api.Name, err)
		log.WarnTag("tcp", "%s", err.Error())
		return
	}
	if call == nil {
		err = fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=无可用服务节点", api.ModuleName, api.Name)
		log.WarnTag("tcp", "%s", err.Error())
		return
	}
	defer func() {
		if err != nil {
			log.WarnTag("tcp", "RPC module=%v serviceName=%v userId=%s error=%v", api.ModuleName, api.Name, GetUserId(ct), err)
		}
	}()
	//这里需要处理超时，避免channel的内存泄漏
	// A7: 原先硬编码 5 秒，现在走 resolveRPCTimeout 查 per-method 覆盖 → 全局默认。
	// 业务可通过 Server.WithMethodTimeout("gate.Login", 10*time.Second) 配置。
	// E1/P2 修复：超时分支原先写 call.Error，与 rpcx client 完成路径对同一字段的
	// 写入构成 data race（-race 可检出）——改用本地 err，in-flight 结果直接放弃。
	select {
	case <-time.After(resolveRPCTimeout(api.ModuleName, api.Name)):
		err = tgf.ErrorRPCTimeOut
	case <-call.Done:
		err = call.Error
	}
	return api.reply, err
}

// SendAsyncRPCMessage [Req, Res any]
// @Description:  异步rpc请求,使用该接口时,需要确保call中的chan被消费, 避免chan的泄露
// E1：本路径接入策略管道。release 在 Call.Done() 消费结果时执行——本 API 的契约
// 本就要求消费 Done（避免 chan 泄漏），并发信号量/熔断计数的释放搭同一约定；
// 不消费 Done 的后果从"chan 泄漏"扩展为"并发配额泄漏"，语义一致。
// 另修：原实现在 xclient.Go 返回 err（call 可能为 nil）时仍包一层 newCall 返回，
// 调用方 Done() 会 nil 解引用 panic——现在错误路径明确返回 (nil, err)。
// @param ct
// @param api
// @return *Call
// @return error
func SendAsyncRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) (*Call, error) {
	release, perr := applyMethodPolicy(api.ModuleName, api.Name)
	if perr != nil {
		return nil, perr
	}
	var (
		done    = make(chan *client.Call, 1)
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)
	if xclient == nil {
		err := fmt.Errorf("找不到对应模块的服务 moduleName=%v", api.ModuleName)
		release(err)
		return nil, err
	}
	call, err := xclient.Go(ct, api.Name, api.args, api.reply, done)
	if err != nil {
		release(err)
		return nil, fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=%w", api.ModuleName, api.Name, err)
	}
	if call == nil {
		err = fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=无可用服务节点", api.ModuleName, api.Name)
		release(err)
		return nil, err
	}
	return newCallWithRelease(call, release), nil
}

// localNoReplyDispatch 是 SendNoReplyRPCMessage(ByAddress) 本地 fast path 的统一
// 后台执行体（E1）：
//   - 用 util.Go + 自带 recover 替代裸 `go func()`——分布式路径 rpcx service.call
//     有 recover，原实现里业务 handler panic 在单进程 no-reply 路径=整个进程崩溃；
//   - release 在调用结束（含 panic 转 error）后执行，策略资源不泄漏。
func localNoReplyDispatch(ct context.Context, moduleName, serviceName string, args, reply any, release func(error)) {
	util.Go(func() {
		var cerr error
		defer func() {
			if r := recover(); r != nil {
				cerr = fmt.Errorf("tgf/rpc: local no-reply dispatch panic %s.%s: %v", moduleName, serviceName, r)
				log.ErrorTag("rpc", "%s", cerr.Error())
			}
			release(cerr)
		}()
		cerr = localDispatcher.Call(ct, moduleName, serviceName, args, reply)
	})
}

// SendNoReplyRPCMessage [Req any, Res any]
//
//	@Description: 发送无需等待返回的rpc消息
//	E1：接入策略管道；Oneshot 的发送错误计入熔断（节点不可达正是要隔离的故障）。
//	@param ct
//	@param api
//	@param Res
//	@return error
func SendNoReplyRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) error {
	release, perr := applyMethodPolicy(api.ModuleName, api.Name)
	if perr != nil {
		return perr
	}
	// 单进程模式 fast path：同 SendRPCMessage 的逻辑，命中 local dispatcher 就
	// 直接反射调用（忽略 reply——no reply 语义）。
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(api.ModuleName); ok {
			localNoReplyDispatch(ct, api.ModuleName, api.Name, api.args, api.reply, release)
			return nil
		}
	}

	var (
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)
	if xclient == nil {
		err := fmt.Errorf("找不到对应模块的服务 moduleName=%v", api.ModuleName)
		release(err)
		return err
	}
	err := xclient.Oneshot(ct, api.Name, api.args)
	release(err)
	return err
}

func SendNoReplyRPCMessageByAddress(moduleName, address, serviceName string, args interface{}) error {
	release, perr := applyMethodPolicy(moduleName, serviceName)
	if perr != nil {
		return perr
	}
	// 单进程模式 fast path：地址参数在单进程下没意义，命中即本地调用。
	// reply 为 nil，Call 内部会构造零值对象占位。
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(moduleName); ok {
			localNoReplyDispatch(context.Background(), moduleName, serviceName, args, nil, release)
			return nil
		}
	}

	var (
		rc      = getRPCClient()
		xclient = rc.getClient(moduleName)
	)
	if xclient == nil {
		err := fmt.Errorf("找不到对应模块的服务 moduleName=%v", moduleName)
		release(err)
		return err
	}
	err := xclient.Oneshot(newRPCNodeContext(moduleName, address), serviceName, args)
	release(err)
	return err
}

// SendRPCMessageByStr 按字符串 module/method 发送同步 RPC（LoginHook/OfflineHook
// 等内部钩子走本路径）。
// E1：接入策略管道 + 超时。原实现是无 deadline 的同步 xclient.Call——慢节点会
// 卡死调用方 goroutine（登录/下线钩子在网关连接的关键路径上）。ct 多为连接级
// *share.Context，不能 context.WithTimeout 包装（破坏 rpcx 类型断言），与
// sendMessage 一致采用 Go + select 模式。
func SendRPCMessageByStr(ct context.Context, moduleName, serviceName string, args, reply interface{}) (err error) {
	release, perr := applyMethodPolicy(moduleName, serviceName)
	if perr != nil {
		return perr
	}
	defer func() {
		release(err)
	}()
	var (
		rc      = getRPCClient()
		xclient = rc.getClient(moduleName)
	)
	if xclient == nil {
		err = tgf.ErrServiceNotFound
		return err
	}
	done := make(chan *client.Call, 1)
	call, gerr := xclient.Go(ct, serviceName, args, reply, done)
	if gerr != nil {
		err = fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=%w", moduleName, serviceName, gerr)
		return err
	}
	if call == nil {
		err = fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=无可用服务节点", moduleName, serviceName)
		return err
	}
	select {
	case <-time.After(resolveRPCTimeout(moduleName, serviceName)):
		err = tgf.ErrorRPCTimeOut
	case <-call.Done:
		err = call.Error
	}
	return err
}

// BorderRPCMessage [Req any, Res any]
//
//	@Description: 推送消息到所有服务节点
//	E1 修复：原实现对 rc.getClient 的返回值不判空——单进程模式 / 目标模块无节点
//	（滚动发布常态）时 xclient 为 nil 直接 panic。现在判空走日志告警路径；
//	同时接入策略管道（Broadcast 的失败计入熔断）。
//	@param ct
//	@param api
//	@param Res]
func BorderRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) {
	release, perr := applyMethodPolicy(api.ModuleName, api.Name)
	if perr != nil {
		log.WarnTag("rpc", "broadcast 被策略拒绝 module=%v method=%v err=%v", api.ModuleName, api.Name, perr)
		return
	}
	var err error
	defer func() {
		release(err)
	}()
	var (
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)
	if xclient == nil {
		err = tgf.ErrServiceNotFound
		log.WarnTag("rpc", "broadcast 找不到对应模块的服务 moduleName=%v", api.ModuleName)
		return
	}
	if err = xclient.Broadcast(ct, api.Name, api.args, api.reply); err != nil {
		log.WarnTag("rpc", "broadcast 失败 module=%v method=%v err=%v", api.ModuleName, api.Name, err)
	}
}

func BorderAllServiceRPCMessageByContext[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) {
	rc := getRPCClient()
	// E1 修复：单进程模式 / client 未初始化时 rc 为 nil，原实现直接 Range panic。
	if rc == nil || rc.clients == nil {
		log.WarnTag("rpc", "broadcast-all 跳过：RPC client 不可用(单进程模式或 discovery 未初始化)")
		return
	}
	rc.clients.Range(func(s string, xClient client.XClient) bool {
		if err := xClient.Oneshot(ct, api.Name, api.args); err != nil {
			log.WarnTag("rpc", "broadcast-all failed module=%v method=%v err=%v", s, api.Name, err)
		}
		return true
	})
}

func BorderAllServiceRPCMessageByContextNotCheck[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) {
	rc := getRPCClient()
	// E1 修复：同 BorderAllServiceRPCMessageByContext 的 nil 防御。
	if rc == nil || rc.clients == nil {
		log.WarnTag("rpc", "broadcast-all 跳过：RPC client 不可用(单进程模式或 discovery 未初始化)")
		return
	}
	rc.clients.Range(func(s string, xClient client.XClient) bool {
		if s == tgf.MonitorServiceModuleName || s == tgf.AdminServiceModuleName {
			return true
		}
		if err := xClient.Oneshot(ct, api.Name, api.args); err != nil {
			log.WarnTag("rpc", "broadcast-all failed module=%v method=%v err=%v", s, api.Name, err)
		}
		return true
	})

}

// SendToGate
// @Description: 发送消息到用户所在的网关
// @param ct
// @param pbMessage
// @return error
func SendToGate(ct context.Context, messageType string, pbMessage proto.Message) error {
	data, err := proto.Marshal(pbMessage)
	req := &ToUserReq{
		Data:        data,
		UserId:      []string{GetUserId(ct)},
		MessageType: messageType,
	}
	if err != nil {
		return err
	}
	err = SendNoReplyRPCMessage[*ToUserReq, *ToUserRes](ct, ToUser.New(req, &ToUserRes{}))
	return err
}

// SendToGateByUserId
// @Description: 根据用户id发送消息到用户所在的网关
// @param userId
// @param pbMessage
// @return error
func SendToGateByUserId(userId, messageType string, pbMessage proto.Message) error {
	data, err := proto.Marshal(pbMessage)
	ct := NewUserRPCContext(userId)
	req := &ToUserReq{
		Data:        data,
		UserId:      []string{GetUserId(ct)},
		MessageType: messageType,
	}
	if err != nil {
		return err
	}
	err = SendNoReplyRPCMessage[*ToUserReq, *ToUserRes](ct, ToUser.New(req, &ToUserRes{}))
	return err
}

// SendToGateSync
//
//	@Description: 发送消息到用户所在的网关同步
//	@param ct
//	@param messageType
//	@param pbMessage
func SendToGateSync(ct context.Context, messageType string, pbMessage proto.Message) error {
	data, err := proto.Marshal(pbMessage)
	req := &ToUserReq{
		Data:        data,
		UserId:      []string{GetUserId(ct)},
		MessageType: messageType,
	}
	if err != nil {
		return err
	}
	_, err = SendRPCMessage(ct, ToUser.New(req, &ToUserRes{}))
	//err = SendNoReplyRPCMessage[*ToUserReq, *ToUserRes](ct, ToUser.New(req, &ToUserRes{}))
	return err
}

// SendAllUserToGate
//
//	@Description: 发送消息到所有用户所在的网关,这个函数不会关注用户所在网关,会广播到所有网关.所以非必要不建议使用该函数,除非你知道你在做什么,并且同时推送的用户数量很大
//	@param messageType
//	@param pbMessage
//	@param userIds
//	@return error
func SendAllUserToGate(messageType string, pbMessage proto.Message, userIds []string) error {
	data, err := proto.Marshal(pbMessage)
	req := &ToUserReq{
		Data:        data,
		UserId:      userIds,
		MessageType: messageType,
	}
	if err != nil {
		return err
	}
	BorderRPCMessage(NewRPCContext(), ToUser.New(req, &ToUserRes{}))
	return err
}

func UserLogin(ctx context.Context, userId string) (*LoginRes, error) {
	return SendRPCMessage(ctx, Login.New(&LoginReq{
		UserId:         userId,
		TemplateUserId: GetTemplateUserId(ctx),
	}, &LoginRes{}))
}

// E6 死代码清理（v3 审计 P3）：下方 NewRPCContext 家族原各有一行
// `ct.SetValue(share.ServerTimeout, 5)`——rpcx 客户端只从 ctx.Deadline() 生成
// wire 的 ServerTimeout 元数据（xclient.go setServerTimeout），服务端从
// req.Metadata 按毫秒解析，全代码库无人读 ctx.Value(share.ServerTimeout)；
// 即便哪天被读，5 也会被按 5 毫秒而非 5 秒解释。已整组删除；
// RPC 超时统一由 resolveRPCTimeout（A7/E1 管道）控制。

func NewCacheUserContext(userId string) context.Context {
	reqMetaDataKey := fmt.Sprintf(tgf.RedisKeyUserNodeMeta, userId)
	reqMetaCacheData, suc := db.GetMap[string, string](reqMetaDataKey)
	ct := share.NewContext(context.Background())
	if suc {
		reqMetaCacheData[tgf.ContextKeyUserId] = userId
		reqMetaCacheData[tgf.ContextKeyRPCType] = tgf.RPCTip
		ct.SetValue(share.ReqMetaDataKey, reqMetaCacheData)
	} else {
		initData := make(map[string]string)
		initData[tgf.ContextKeyUserId] = userId
		initData[tgf.ContextKeyRPCType] = tgf.RPCTip
		ct.SetValue(share.ReqMetaDataKey, initData)
	}
	return ct
}

func NewRPCContext() context.Context {
	return newRPCContext(context.Background())
}

func newRPCContext(parent context.Context) context.Context {
	ct := share.NewContext(parent)
	initData := make(map[string]string)
	initData[tgf.ContextKeyRPCType] = tgf.RPCTip
	ct.SetValue(share.ReqMetaDataKey, initData)
	return ct
}

func newRPCNodeContext(moduleName, address string) context.Context {
	ct := share.NewContext(context.Background())
	initData := make(map[string]string)
	initData[tgf.ContextKeyRPCType] = tgf.RPCTip
	initData[moduleName] = address
	ct.SetValue(share.ReqMetaDataKey, initData)
	return ct
}

// NewUserRPCContext
// @Description: instantiate rpc context with user id
// @param userId
// @return context.Context
func NewUserRPCContext(userId string) context.Context {
	ct := share.NewContext(context.Background())
	initData := make(map[string]string)
	initData[tgf.ContextKeyRPCType] = tgf.RPCTip
	initData[tgf.ContextKeyUserId] = userId
	initData[tgf.ContextKeyHash] = userId
	ct.SetValue(share.ReqMetaDataKey, initData)
	return ct
}

// NewBindRPCContext
// @Description: instantiate rpc context with binding, all user binging same node id.
// @param userId
// @return context.Context
func NewBindRPCContext(userId ...string) context.Context {
	ct := share.NewContext(context.Background())
	initData := make(map[string]string)
	initData[tgf.ContextKeyRPCType] = tgf.RPCBroadcastTip
	ids := strings.Join(userId, ",")
	initData[tgf.ContextKeyBroadcastUserIds] = ids
	ct.SetValue(share.ReqMetaDataKey, initData)
	return ct
}
