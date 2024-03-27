package rpc

import (
	"context"
	"errors"
	"fmt"
	"github.com/cornelk/hashmap"
	client2 "github.com/thkhxm/rpcx-consul/client"
	"github.com/thkhxm/rpcx/client"
	"github.com/thkhxm/rpcx/server"
	"github.com/thkhxm/rpcx/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/component"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc/internal"
	"github.com/thkhxm/tgf/util"
	"google.golang.org/protobuf/proto"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"
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
type Server struct {
	rpcServer *server.Server

	//启动后执行的操作
	afterOptionals []Optional
	//启动前执行
	beforeOptionals []Optional
	//
	maxWorkers  int
	maxCapacity int
	//
	service   []IService
	closeChan chan bool
	//
	minPort int32
	maxPort int32

	enableProfile bool

	customServiceAddress bool

	//

	//
	whiteServiceList []string
}

type Optional func(*Server)

func (s *Server) withConsulDiscovery() *Server {
	var ()
	s.beforeOptionals = append(s.beforeOptionals, func(server *Server) {
		internal.UseConsulDiscovery()
	})
	return s
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

// withServiceClient
//
//	@Description: 注册rpcx的客户端程序
//	@receiver this
func (s *Server) withServiceClient() *Server {
	var ()
	//_
	s.afterOptionals = append(s.afterOptionals, func(server *Server) {
		c := newRPCClient().startup()
		log.InfoTag("init", "装载RPCClient服务")
		if len(server.whiteServiceList) > 0 {
			for _, messageType := range server.whiteServiceList {
				c.AddWhiteService(messageType)
				log.InfoTag("init", "加入请求无需登录的白名单 serviceName=%v", messageType)
			}
		}
	})
	return s
}

func (s *Server) WithGateway(port string, hook IUserHook) *Server {
	var ()
	s.beforeOptionals = append(s.beforeOptionals, func(server *Server) {
		builder := newTCPBuilder()
		builder.WithPort(port)
		builder.SetUserHook(hook)
		gateway := GatewayService(builder)
		s.service = append(s.service, gateway)
		log.InfoTag("init", "装载逻辑服务[%v@%v]", gateway.GetName(), gateway.GetVersion())
	})
	return s
}

func (s *Server) WithGatewayWSS(port, path, key, cert string) *Server {
	var ()
	s.beforeOptionals = append(s.beforeOptionals, func(server *Server) {
		builder := newTCPBuilder()
		builder.WithPort(port)
		builder.WithWSPath(path)
		builder.WithWss(key, cert)
		userHook := &UserHook{}
		for _, service := range server.service {
			if service.GetUserHook() == nil {
				continue
			}
			for _, hook := range service.GetUserHook().GetLoginHooks() {
				userHook.AddLoginHook(hook)
			}
			for _, hook := range service.GetUserHook().GetOfflineHooks() {
				userHook.AddOfflineHook(hook)
			}
		}
		builder.SetUserHook(userHook)
		gateway := GatewayService(builder)
		s.service = append(s.service, gateway)
		log.InfoTag("init", "装载逻辑服务[%v@%v]", gateway.GetName(), gateway.GetVersion())
	})
	return s
}

func (s *Server) WithGatewayWS(port, path string) *Server {
	var ()
	s.beforeOptionals = append(s.beforeOptionals, func(server *Server) {
		builder := newTCPBuilder()
		builder.WithPort(port)
		builder.WithWSPath(path)
		userHook := &UserHook{}
		for _, service := range server.service {
			if service.GetUserHook() == nil {
				continue
			}
			for _, hook := range service.GetUserHook().GetLoginHooks() {
				userHook.AddLoginHook(hook)
			}
			for _, hook := range service.GetUserHook().GetOfflineHooks() {
				userHook.AddOfflineHook(hook)
			}
		}
		builder.SetUserHook(userHook)
		gateway := GatewayService(builder)
		s.service = append(s.service, gateway)
		log.InfoTag("init", "装载逻辑服务[%v@%v]", gateway.GetName(), gateway.GetVersion())
	})
	return s
}

func (s *Server) WithProfileDebug() *Server {
	s.enableProfile = true
	return s
}

func (s *Server) Run() chan bool {
	var (
		serviceName    string
		ip             string
		_logServiceMsg string
	)

	//运行数据初始化
	db.Run()

	//开启服务器模式
	tgf.ServerModule = true
	// TODO 如果有需要，可以对Optional进行优先级的控制，控制加载顺序
	for _, beforeOptional := range s.beforeOptionals {
		beforeOptional(s)
	}
	/**启动逻辑链*/
	//注册rpcx服务
	s.rpcServer = server.NewServer(server.WithPool(s.maxWorkers, s.maxCapacity))
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

	//启动后执行的业务
	for _, afterOptional := range s.afterOptionals {
		afterOptional(s)
	}

	//启用服务,使用tcp
	log.InfoTag("init", "rpcx服务启动成功 addr=%v service=[%v] ", _logServiceMsg, ip)
	return s.closeChan
}

func (s *Server) Destroy() {
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
	rpcServer.closeChan = make(chan bool, 1)

	//
	rpcServer.withConsulDiscovery()
	rpcServer.withServiceClient()
	//
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
		return errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v", moduleName))
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

	if xclient == nil {
		err = errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v serviceName=%v", api.ModuleName, api.Name))
		log.WarnTag("tcp", err.Error())
		return
	}
	call, err := xclient.Go(ct, api.Name, api.args, api.reply, done)
	if err != nil {
		err = errors.New(fmt.Sprintf("rpc请求异常 moduleName=%v serviceName=%v error=%v", api.ModuleName, api.Name, err))
		log.WarnTag("tcp", err.Error())
		return
	}
	//这里需要处理超时，避免channel的内存泄漏
	select {
	case <-time.After(time.Second * 5):
		call.Error = tgf.ErrorRPCTimeOut
		break
	case <-call.Done:
		break
	}

	defer func() {
		if call.Error != nil {
			log.WarnTag("tcp", "RPC module=%v serviceName=%v error=%v", api.ModuleName, api.Name, call.Error)
		}
	}()
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
		return errors.New(fmt.Sprintf("找不到对应模块的服务 moduleName=%v", moduleName))
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
	nodeMap := ct.Value(share.ReqMetaDataKey)
	if m, h := nodeMap.(map[string]string); h {
		rc.clients.Range(func(s string, xClient client.XClient) bool {
			if m[s] != "" {
				xClient.Oneshot(ct, api.Name, api.args)
			}
			return true
		})
	}
}

func BorderAllServiceRPCMessageByContextNotCheck[Req any, Res any](ct context.Context, api *ServiceAPI[Req, Res]) {
	var (
		rc = getRPCClient()
		//xclient = rc.getClient(api.ModuleName)
	)
	nodeMap := ct.Value(share.ReqMetaDataKey)
	if _, h := nodeMap.(map[string]string); h {
		rc.clients.Range(func(s string, xClient client.XClient) bool {
			if s == tgf.MonitorServiceModuleName || s == tgf.AdminServiceModuleName {
				return true
			}
			xClient.Oneshot(ct, api.Name, api.args)
			return true
		})
	}
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
		UserId:      GetUserId(ct),
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
	ct := NewCacheUserContext(userId)
	req := &ToUserReq{
		Data:        data,
		UserId:      GetUserId(ct),
		MessageType: messageType,
	}
	if err != nil {
		return err
	}
	err = SendNoReplyRPCMessage[*ToUserReq, *ToUserRes](ct, ToUser.New(req, &ToUserRes{}))
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
