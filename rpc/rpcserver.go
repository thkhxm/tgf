package rpc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cornelk/hashmap"
	client2 "github.com/thkhxm/rpcx-consul/client"
	"github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/rpcx/server"
	"github.com/thkhxm/rpcx/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/component"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/metrics"
	"github.com/thkhxm/tgf/rpc/internal"
	"github.com/thkhxm/tgf/trace"
	"github.com/thkhxm/tgf/util"
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

	// A6 新增：后台健康心跳骨架。
	// healthInterval > 0 时 Run 会启动一个心跳 goroutine，每 healthInterval
	// 打一次"进程存活"的 heartbeat（当前只做 atomic 状态位 + 日志，未来 C2/B4
	// 集成 Consul Agent TTL check / Prometheus gauge 时复用这条 goroutine）。
	healthInterval time.Duration
	// healthCheckStop 是心跳 goroutine 的退出信号，Destroy 时关闭。
	healthCheckStop chan struct{}
	// healthy 是进程级存活标志，默认 false，心跳 goroutine 启动后首次 tick 前就会置 true。
	healthy atomic.Bool

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

// WithHealthCheck 开启进程级健康心跳。interval 指定每次心跳的间隔；传 0 或负数
// 视为禁用（默认行为就是禁用）。
//
// A6 定位是"骨架"：当前心跳只做两件事——
//  1. 通过 atomic.Bool 维护 `healthy` 状态，业务可通过 Server.IsHealthy() 观察；
//  2. 每次 tick 打一条 DebugTag 日志，便于运维从日志里确认进程存活。
//
// 后续 C2（Consul 改造）/ B4（可观测性）会在这条心跳 goroutine 上挂上真正的
// Consul Agent TTL check 续约调用 + Prometheus gauge 上报。
//
// 由于是骨架，本 Option 的默认 interval 建议用 5~10 秒；再短容易干扰 Consul
// 的 rpcx UpdateInterval。
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
//   - 后续 tick 每次都写日志 + 可以在这里接入外部 ping / TTL check
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
	s.healthy.Store(true)

	util.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// A6 骨架：当前只打日志。C2/B4 在这里接 Consul agent check 续约
				// 和 Prometheus gauge 上报。
				log.DebugTag("health", "heartbeat tick nodeId=%v healthy=%v",
					tgf.NodeId, s.healthy.Load())
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
			c := newRPCClient().startup()
			// D4/D5: startup 在 discovery 缺失或 Consul 不可达时返回 nil——
			// 此时跳过白名单装载，避免 nil 解引用；后续 getRPCClient 会重试。
			if c == nil {
				log.WarnTag("init", "RPCClient 启动失败,白名单暂未装载(将随 client 重试)")
				return
			}
			log.InfoTag("init", "装载RPCClient服务")
			for _, messageType := range sv.whiteServiceList {
				c.AddWhiteService(messageType)
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
	port := tgf.GetStrConfig[string](tgf.EnvironmentServicePort)
	if s.minPort > 0 && s.maxPort > s.minPort {
		port = fmt.Sprintf("%v", rand.Int31n(s.maxPort-s.minPort)+s.minPort)
	}
	//rpcx加入服务发现组件
	local := util.GetLocalHost()
	if s.customServiceAddress {
		local = tgf.GetStrConfig[string](tgf.EnvironmentServiceAddress)
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

	// 如果要向 discovery 注册，先装 plugin 并追加 MonitorService——MonitorService
	// 只在 discovery 开启时存在。
	if discovery != nil {
		// D3：持有 Consul 注册插件引用，优雅停机时调用其 Stop() 完成
		// 「KV 反注册 + 停止 TTL 刷新 goroutine」（见 Destroy / unregisterFromConsul）。
		s.registryPlugin = discovery.RegisterServer(ip)
		s.rpcServer.Plugins.Add(s.registryPlugin)
		s.rpcServer.Plugins.Add(NewRPCXServerHandler())
		s.service = append(s.service, &MonitorService{})
	}

	// 统一遍历：所有 service 都要 Startup；额外的 RegisterName 只在 discovery 非 nil 时做。
	for _, service := range s.service {
		serviceName = fmt.Sprintf("%v", service.GetName())
		metaData := fmt.Sprintf("version=%s&nodeId=%s", service.GetVersion(), tgf.NodeId)

		if discovery != nil {
			if err := s.rpcServer.RegisterName(serviceName, service, metaData); err != nil {
				log.Error("[init] 注册服务发现失败 serviceName=%v metaDat=%v error=%v", serviceName, metaData, err)
				continue
			}
		}

		if startupOK, startupErr := service.Startup(); !startupOK {
			log.Error("[init] 服务启动异常 serviceName=%v error=%v", serviceName, startupErr)
			continue
		}

		_logServiceMsg += serviceName + " " + metaData + ","
		if discovery != nil {
			log.InfoTag("init", "注册服务发现 serviceName=%v metaDat=%v", serviceName, metaData)
		} else {
			log.InfoTag("init", "服务启动 serviceName=%v metaDat=%v (无 discovery)", serviceName, metaData)
		}
	}

	// 单进程模式：Startup 完成后把 service 注册到本地 dispatcher。
	// 放在 Startup 之后是因为 Startup 可能修改 service 内部状态（比如 rpc.Module 的 State 字段）。
	s.registerLocalServices()
	// D4: 单进程模式下白名单不再依赖 rpcClient（它不启动）——把 WithWhiteService
	// 注册的白名单同步给网关本地直通路径（gateway_local_dispatch.go）。
	if s.inProcessDispatch {
		setLocalGateWhiteList(s.whiteServiceList)
	}

	util.Go(func() {
		if err := s.rpcServer.Serve("tcp", ip); err != nil {
			// D3 / P0-3：优雅停机时 rpcServer.Shutdown 会关闭 listener，
			// Serve 返回 ErrServerClosed——这是停机的正常路径，绝不能在这里
			// os.Exit 把仍在执行的停机序列（drain/终末 flush）直接杀死。
			if errors.Is(err, server.ErrServerClosed) {
				log.InfoTag("shutdown", "rpcx listener 已关闭(优雅停机) addr=%v", ip)
				return
			}
			log.Error("[init] rpcx服务启动异常 serviceName=%v addr=%v err=%v", serviceName, ip, err)
			// D3：启动/监听失败必须以非零码退出（原实现 os.Exit(0) 会让
			// 容器编排/发布系统把启动失败误判为正常退出）。
			os.Exit(1)
			return
		}
	})

	// A4: 走 buildPostServeHooks——用户 Hook 先跑，之后默认 RPC Client watch（除非被关闭）。
	for _, hook := range s.buildPostServeHooks() {
		hook(s)
	}

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
//  3. 网关停止 accept 并关闭 TCP/WS/KCP listener（已建立的 TCP/WS 连接不受影响，
//     KCP 例外：kcp-go 会话与 listener 共享 UDP socket，关 listener 会中断既有会话）；
//  4. drain in-flight——rpcServer.Shutdown 等待正在处理的 RPC 全部完成（带可配置
//     超时，见 WithShutdownDrainTimeout）；
//  5. 第一轮终末 flush——把 drain 完成时刻的全部脏数据立即落库（db.FlushAll）；
//  6. 业务 service Destroy（每个独立 recover，单个 panic 不中断其余）。
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
	s.stopGatewayAccept()
	s.drainInFlight()
	shutdownFlushFn()
	for _, service := range s.service {
		destroyServiceSafely(service)
	}
}

// unregisterFromConsul 调用 Consul 注册插件的 Stop()：删除本节点全部服务的 KV
// 注册并停止 TTL 刷新 goroutine。带 consulUnregisterTimeout 超时兜底——
// Consul 不可达时记日志继续停机（节点 KV 随 TTL 过期），不阻塞后续 drain/flush。
func (s *Server) unregisterFromConsul() {
	type stopper interface{ Stop() error }
	sp, ok := s.registryPlugin.(stopper)
	if !ok {
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

var rpcClient *Client

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
func (c *ClientOptional) startup() *Client {
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
	newClient.whiteMethod = make([]string, 0)

	//获取当前已经注册了的服务
	for _, v := range baseDiscovery.GetServices() {
		if strings.Index(v.Key, "/") > 0 {
			newClient.registerClient(discovery, strings.Split(v.Key, "/")[0])
		}
	}
	newClient.watchBaseDiscovery(discovery, baseDiscovery)
	// 完整构造后最后发布
	rpcClient = newClient
	return rpcClient
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
		for {
			select {
			case kv := <-discovery.WatchService():
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

	if rpcClient == nil {
		singletonLock.Lock()
		defer singletonLock.Unlock()
		if rpcClient == nil {
			if c := newRPCClient().startup(); c != nil {
				log.InfoTag("init", "装载RPCClient服务")
			}
		}
	}
	return rpcClient
}

type Call struct {
	rpcxCall *client.Call
}

func newCall(rpcxCall *client.Call) (call *Call) {
	call = &Call{}
	call.rpcxCall = rpcxCall
	return
}

// Done
//
//	@Description: 会阻塞
//	@receiver this
//	@return error
func (this *Call) Done() error {
	var ()
	cal := <-this.rpcxCall.Done
	return cal.Error
}

func sendMessage(ct IUserConnectData, moduleName, serviceName string, args, reply interface{}) error {
	// D4 / P0-2 修复：单进程模式（WithSingleProcess / WithStandalone+WithInProcessDispatch）
	// 下 discovery 为 nil 且 rpcClient 不启动，原实现首条客户端消息走 getRPCClient()
	// → startup() → 对 nil discovery 调方法直接 panic，网关 100% 不可用。
	// 现在：本地 dispatcher 命中时走进程内直通（登录态/白名单语义与分布式路径一致），
	// 见 gateway_local_dispatch.go。
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(moduleName); ok {
			if !ct.IsLogin() && !checkLocalGateWhiteList(moduleName+"."+serviceName) {
				return errors.New(fmt.Sprintf("用户未登录 非白名单请求无法抵达 moduleName=%v serviceName=%v", moduleName, serviceName))
			}
			return localGateDispatch(ct.GetContextData(), moduleName, serviceName, args, reply)
		}
	}

	var (
		rc      = getRPCClient()
		xclient client.XClient
	)
	// D4: rpcClient 不可用（单进程/Standalone 模式、或 discovery 创建失败）时
	// 返回明确错误而非 panic。
	if rc == nil {
		return errors.New(fmt.Sprintf("RPC client 不可用(单进程模式下模块未注册到本地 dispatcher,或 discovery 未初始化) moduleName=%v serviceName=%v", moduleName, serviceName))
	}
	xclient = rc.getClient(moduleName)
	if xclient == nil {
		return errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v serviceName=%v ", moduleName, serviceName))
	}
	if ct.IsLogin() || rc.CheckWhiteList(moduleName+"."+serviceName) {
		err := xclient.Call(ct.GetContextData(), serviceName, args, reply)
		return err
	}
	return errors.New(fmt.Sprintf("用户未登录 非白名单请求无法抵达 moduleName=%v serviceName=%v", moduleName, serviceName))
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
	var policyRelease func(error) = noopRelease
	if mr := resolveMethodPolicy(api.ModuleName, api.Name); mr != nil {
		var perr error
		policyRelease, perr = mr.beforeCall()
		if perr != nil {
			err = perr
			return
		}
	}
	defer func() {
		policyRelease(err)
	}()

	// 单进程模式 fast path：如果开启了 in-process dispatch 且 module 在本地
	// 注册，直接反射调用。绕开 rpcx + Consul 的全部网络路径。
	//
	// 注意 reply 的分配问题：ServiceAPI.NewRPC 对指针类型的 Res 会生成一个
	// typed nil pointer（如 (*GiveGiftRes)(nil)）。dispatcher.Call 内部的
	// ensureAllocated 会给它分配真正的 struct，但分配出来的对象是 Call 内部
	// 的局部变量——api.reply 本身仍然是 nil。解决方式：在调 Call 之前就在这里
	// 用反射分配好真正的 reply 对象，传给 Call 让 method 写入，然后用 type
	// assertion 转回 Res 返回。
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
		err = fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=%v", api.ModuleName, api.Name, err)
		log.WarnTag("tcp", "%s", err.Error())
		return
	}
	if call == nil {
		err = fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=无可用服务节点", api.ModuleName, api.Name)
		log.WarnTag("tcp", "%s", err.Error())
		return
	}
	defer func() {
		if call.Error != nil {
			log.WarnTag("tcp", "RPC module=%v serviceName=%v userId=%s error=%v", api.ModuleName, api.Name, GetUserId(ct), call.Error)
		}
	}()
	//这里需要处理超时，避免channel的内存泄漏
	// A7: 原先硬编码 5 秒，现在走 resolveRPCTimeout 查 per-method 覆盖 → 全局默认。
	// 业务可通过 Server.WithMethodTimeout("gate.Login", 10*time.Second) 配置。
	select {
	case <-time.After(resolveRPCTimeout(api.ModuleName, api.Name)):
		call.Error = tgf.ErrorRPCTimeOut
	case <-call.Done:
	}
	return api.reply, call.Error
}

// SendAsyncRPCMessage [Req, Res any]
// @Description:  异步rpc请求,使用该接口时,需要确保call中的chan被消费, 避免chan的泄露
// @param ct
// @param api
// @return *client.Call
// @return error
func SendAsyncRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) (*Call, error) {
	var (
		done    = make(chan *client.Call, 1)
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)
	if xclient == nil {
		return nil, errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v", api.ModuleName))
	}
	call, err := xclient.Go(ct, api.Name, api.args, api.reply, done)
	return newCall(call), err
}

// SendNoReplyRPCMessage [Req any, Res any]
//
//	@Description: 发送无需等待返回的rpc消息
//	@param ct
//	@param api
//	@param Res
//	@return error
func SendNoReplyRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) error {
	// 单进程模式 fast path：同 SendRPCMessage 的逻辑，命中 local dispatcher 就
	// 直接反射调用（忽略 reply——no reply 语义）。
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(api.ModuleName); ok {
			// 后台执行避免阻塞调用方——和 rpcx Oneshot 的语义保持一致
			go func() {
				_ = localDispatcher.Call(ct, api.ModuleName, api.Name, api.args, api.reply)
			}()
			return nil
		}
	}

	var (
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)
	if xclient == nil {
		return errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v", api.ModuleName))
	}
	err := xclient.Oneshot(ct, api.Name, api.args)
	return err
}

func SendNoReplyRPCMessageByAddress(moduleName, address, serviceName string, args interface{}) error {
	// 单进程模式 fast path：地址参数在单进程下没意义，命中即本地调用。
	// reply 为 nil，Call 内部会构造零值对象占位。
	if localDispatchEnabled.Load() {
		if _, ok := localDispatcher.Lookup(moduleName); ok {
			go func() {
				_ = localDispatcher.Call(context.Background(), moduleName, serviceName, args, nil)
			}()
			return nil
		}
	}

	var (
		rc      = getRPCClient()
		xclient = rc.getClient(moduleName)
	)
	if xclient == nil {
		return errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v", moduleName))
	}
	err := xclient.Oneshot(newRPCNodeContext(moduleName, address), serviceName, args)
	return err
}

func SendRPCMessageByStr(ct context.Context, moduleName, serviceName string, args, reply interface{}) error {
	var (
		rc      = getRPCClient()
		xclient = rc.getClient(moduleName)
	)
	if xclient == nil {
		return tgf.ServiceNotFound
	}
	err := xclient.Call(ct, serviceName, args, reply)
	return err
}

// BorderRPCMessage [Req any, Res any]
//
//	@Description: 推送消息到所有服务节点
//	@param ct
//	@param api
//	@param Res]
func BorderRPCMessage[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) {
	var (
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)
	xclient.Broadcast(ct, api.Name, api.args, api.reply)
}

func BorderAllServiceRPCMessageByContext[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) {
	var (
		rc = getRPCClient()
		//xclient = rc.getClient(api.ModuleName)
	)
	//nodeMap := ct.Value(share.ReqMetaDataKey)
	//if m, h := nodeMap.(map[string]string); h {
	rc.clients.Range(func(s string, xClient client.XClient) bool {
		//if m[s] != "" {
		xClient.Oneshot(ct, api.Name, api.args)
		//}
		return true
	})
	//}
}

func BorderAllServiceRPCMessageByContextNotCheck[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) {
	var (
		rc = getRPCClient()
		//xclient = rc.getClient(api.ModuleName)
	)
	rc.clients.Range(func(s string, xClient client.XClient) bool {
		if s == tgf.MonitorServiceModuleName || s == tgf.AdminServiceModuleName {
			return true
		}
		xClient.Oneshot(ct, api.Name, api.args)
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

func newUserContext(userId string) context.Context {
	ct := share.NewContext(context.Background())
	initData := make(map[string]string)
	initData[tgf.ContextKeyUserId] = userId
	ct.SetValue(share.ReqMetaDataKey, initData)
	return ct
}

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
	ct.SetValue(share.ServerTimeout, 5)
	return ct
}

func NewRPCContext() context.Context {
	ct := share.NewContext(context.Background())
	initData := make(map[string]string)
	initData[tgf.ContextKeyRPCType] = tgf.RPCTip
	ct.SetValue(share.ReqMetaDataKey, initData)
	ct.SetValue(share.ServerTimeout, 5)
	return ct
}

func newRPCNodeContext(moduleName, address string) context.Context {
	ct := share.NewContext(context.Background())
	initData := make(map[string]string)
	initData[tgf.ContextKeyRPCType] = tgf.RPCTip
	initData[moduleName] = address
	ct.SetValue(share.ReqMetaDataKey, initData)
	ct.SetValue(share.ServerTimeout, 5)
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
	ct.SetValue(share.ServerTimeout, 5)
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
	ct.SetValue(share.ServerTimeout, 5)
	return ct
}
