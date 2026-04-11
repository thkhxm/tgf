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
	client2 "github.com/rpcxio/rpcx-consul/client"
	"github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/server"
	"github.com/smallnest/rpcx/share"
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

	// A6 新增：后台健康心跳骨架。
	// healthInterval > 0 时 Run 会启动一个心跳 goroutine，每 healthInterval
	// 打一次"进程存活"的 heartbeat（当前只做 atomic 状态位 + 日志，未来 C2/B4
	// 集成 Consul Agent TTL check / Prometheus gauge 时复用这条 goroutine）。
	healthInterval time.Duration
	// healthCheckStop 是心跳 goroutine 的退出信号，Destroy 时关闭。
	healthCheckStop chan struct{}
	// healthy 是进程级存活标志，默认 false，心跳 goroutine 启动后首次 tick 前就会置 true。
	healthy atomic.Bool
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
		if ss.GetLogicSyncMethod() == nil {
			continue
		}
		for _, lm := range ss.GetLogicSyncMethod() {
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

	//如果加入了服务注册，那么走服务注册的流程
	if discovery != nil {
		s.rpcServer.Plugins.Add(discovery.RegisterServer(ip))
		s.rpcServer.Plugins.Add(NewRPCXServerHandler())
		s.service = append(s.service, &MonitorService{})
		//注册服务到服务发现上,允许多个服务，注册到一个节点
		for _, service := range s.service {
			serviceName = fmt.Sprintf("%v", service.GetName())
			metaData := fmt.Sprintf("version=%s&nodeId=%s", service.GetVersion(), tgf.NodeId)
			err := s.rpcServer.RegisterName(serviceName, service, metaData)
			if err != nil {
				log.Error("[init] 注册服务发现失败 serviceName=%v metaDat=%v error=%v", serviceName, metaData, err)
				continue
			}

			if startupOK, startupErr := service.Startup(); !startupOK {
				log.Error("[init] 服务启动异常 serviceName=%v error=%v", serviceName, startupErr)
				continue
			}
			_logServiceMsg += serviceName + " " + metaData + ","
			log.InfoTag("init", "注册服务发现 serviceName=%v metaDat=%v", serviceName, metaData)
		}
	}

	util.Go(func() {
		if err := s.rpcServer.Serve("tcp", ip); err != nil {
			log.Error("[init] rpcx务启动异常 serviceName=%v addr=%v err=%v", serviceName, ip, err)
			os.Exit(0)
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

func (s *Server) Destroy() {
	// A6: 先停心跳 goroutine，再跑业务 service 的 Destroy。顺序原因：service Destroy
	// 过程可能需要 "进程还活着" 的前提（例如通过 RPC 把 in-flight 请求 drain 完），
	// 心跳仍能观察到；但业务 Destroy 完成后心跳就不应再跳。
	s.stopHealthCheckLoop()
	for _, service := range s.service {
		service.Destroy(service)
	}
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
func (c *ClientOptional) startup() *Client {
	var ()
	//
	rpcClient = new(Client)
	rpcClient.clients = hashmap.New[string, client.XClient]()
	rpcClient.whiteMethod = make([]string, 0)

	//注册一个basePath的路径
	discovery := internal.GetDiscovery()
	baseDiscovery := discovery.RegisterDiscovery("")
	//获取当前已经注册了的服务
	for _, v := range baseDiscovery.GetServices() {
		if strings.Index(v.Key, "/") > 0 {
			rpcClient.registerClient(discovery, strings.Split(v.Key, "/")[0])
		}
	}
	rpcClient.watchBaseDiscovery(discovery, baseDiscovery)
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
	if val, ok := c.clients.Get(moduleName); ok {
		xclient = val
	}
	return
}

func getRPCClient() *Client {

	if rpcClient == nil {
		singletonLock.Lock()
		defer singletonLock.Unlock()
		if rpcClient == nil {
			newRPCClient().startup()
			log.InfoTag("init", "装载RPCClient服务")
		}
		//log.Warn("[rpc] RPCClient没有初始化,清调用rpc.NewRPCClient函数进行实例化")
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
	var (
		rc      = getRPCClient()
		xclient = rc.getClient(moduleName)
	)
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
	var (
		done    = make(chan *client.Call, 1)
		rc      = getRPCClient()
		xclient = rc.getClient(api.ModuleName)
	)

	// B4 埋点：进入前记录起始时间戳；任何返回路径 defer 里观测延迟 + 累加计数/错误。
	startTime := time.Now()
	defer func() {
		observeRPCCall(api.ModuleName, api.Name, startTime, err)
	}()

	if xclient == nil {
		err = fmt.Errorf("找不到对应模块的服务 moduleName=%v serviceName=%v", api.ModuleName, api.Name)
		log.WarnTag("tcp", "%s", err.Error())
		return
	}
	call, err := xclient.Go(ct, api.Name, api.args, api.reply, done)
	defer func() {
		if call.Error != nil {
			log.WarnTag("tcp", "RPC module=%v serviceName=%v userId=%s error=%v", api.ModuleName, api.Name, GetUserId(ct), call.Error)
		}
	}()
	if err != nil {
		err = fmt.Errorf("rpc请求异常 moduleName=%v serviceName=%v error=%v", api.ModuleName, api.Name, err)
		log.WarnTag("tcp", "%s", err.Error())
		return
	}
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
