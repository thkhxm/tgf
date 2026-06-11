package rpc

import (
	"github.com/thkhxm/tgf/v2"
	"github.com/thkhxm/tgf/v2/log"
	"golang.org/x/net/context"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/26
//***************************************************

// GateService
// @Description: 默认网关。
// A8: 在 TCP/WS 基础上支持可选的 KCP listener——同一个 GateService 实例复用
// 同一套 users 表和 handleConn 逻辑。通过 Server.WithGatewayKCP 配置。
type GateService struct {
	Module
	tcpBuilder ITCPBuilder
	kcpBuilder IKCPBuilder // A8: nil 表示不启用 KCP listener
	tcpService ITCPService
	// localAddressFn 覆盖本实例的 gate 地址（F3，仅测试用，见 localAddress）。
	localAddressFn func() string
}

func (g *GateService) GetName() string {
	return tgf.GatewayServiceModuleName
}

func (g *GateService) GetVersion() string {
	return "1.0"
}

func (g *GateService) Startup() (bool, error) {
	g.tcpService = newDefaultTCPServer(g.tcpBuilder)
	g.tcpService.Run()
	// A8: 如果额外配了 KCP listener，在 TCP/WS listener 启动后再起一个 KCP accept loop。
	// 底层 TCPServer 实例复用，users / handleConn / doLogic 全部共享——
	// 从 A3 的视角看，同一个 uid 通过 TCP 或 KCP 登录走的是同一套 Redis 锁和 owner meta。
	if g.kcpBuilder != nil {
		if concrete, ok := g.tcpService.(*TCPServer); ok {
			if err := concrete.startKCPListener(g.kcpBuilder); err != nil {
				log.WarnTag("init", "KCP网关启动失败 err=%v", err)
			}
		} else {
			log.WarnTag("init", "tcpService 不是 *TCPServer,无法附加 KCP listener")
		}
	}
	return true, nil
}

// StopAccept 停止网关接收新连接并关闭全部 listener（TCP/WS/KCP）。幂等。
// D 档（v3）暴露给优雅停机编排（Wave2 / Server.Destroy）的钩子：
// 编排方应当先调用本方法停止 accept，再做 in-flight drain 与在线连接的 Offline 清理。
// 在 Startup 之前调用（tcpService 尚未创建）安全返回 nil。
func (g *GateService) StopAccept() error {
	if g.tcpService == nil {
		return nil
	}
	return g.tcpService.CloseListeners()
}

func (g *GateService) UploadUserNodeInfo(ctx context.Context, args *UploadUserNodeInfoReq, reply *UploadUserNodeInfoRes) error {
	var ()
	if ok := g.tcpService.UpdateUserNodeInfo(args.UserId, args.ServicePath, args.NodeId); !ok {
		reply.ErrorCode = -1
	}
	log.DebugTag("gate", "修改用户节点信息 userId=%v servicePath=%v nodeId=%v res=%v", args.UserId, args.ServicePath, args.NodeId, reply)
	return nil
}

// Login 是 gate 模块的登录入口。
//
// A3-phase2 改造：原先逻辑是"先本地 Offline，失败广播 Offline 给所有节点"，
// 有三个问题：广播不精准（打扰无关节点）、没有互斥（两个节点同时登同一 uid）、
// 没有等待远端清理就继续本地 DoLogin。
//
// D7 / P0-6（v3）：登录前强制凭据校验（fail-closed）。原实现直接信任客户端
// 自报的 args.UserId 完成身份绑定——任何 socket 可冒充任意账号。现在 LoginReq
// 必须携带 Token，由 ILoginCheck（默认 HMAC token，可注入自定义）校验并比对
// 身份；旧的无鉴权行为需显式 Server.WithoutLoginCheck() 才保留。
// 详见 login_check.go。
//
// 新流程：
//  0. checkLoginCredential 校验 args.Token（D7，失败立即拒绝）。
//  1. 通过 Redis 分布式锁（key = tgf:gate:login:lock:<uid>）在 phase1 CAS 之上
//     再加一层跨节点互斥——只有拿到锁的节点才能进入登录流程。
//     F3：锁带 watchdog 续期；无 Redis 部署降级为进程内锁（见 login_lock.go）。
//  2. 锁内读 user:node:meta 里的 gate owner：
//     - 本地拥有 → 走本地 Offline（phase1 CAS 保证清理幂等）。
//     - 远端拥有 → 定向 RPC 到 owner 节点发 Offline（不再广播）。
//     F3：该 RPC 同步带 ack——远端完成全部清理（含旧会话 OfflineHook）后才返回，
//     取代原先 Oneshot + sleep(200ms) 的时序赌博（审计：本端 200ms < 远端 1s）。
//     - 无 owner → 跳过踢人。
//  3. 本地 DoLogin（phase1 CAS 保证 Idle→LoggingIn→Online）。
//     F3：携带 args.ResumeToken 用于断线重连续联（窗口内缓冲补发），并签发
//     本次会话的新 resume token 经 reply.ResumeToken 返还给业务转交客户端。
//  4. 把本地 gate 地址写回 user:node:meta，用于下一次登录时的 owner 判定。
//  5. defer 释放锁。
func (g *GateService) Login(ctx context.Context, args *LoginReq, reply *LoginRes) error {
	// D7: 凭据校验放在登录锁之前——非法请求不应消耗分布式锁与踢人流程。
	if credErr := checkLoginCredential(args); credErr != nil {
		reply.ErrorCode = -1
		log.WarnTag("gate", "login credential rejected uid=%v err=%v", args.UserId, credErr)
		return credErr
	}

	lockHandle, err := loginCoord.AcquireLoginLock(args.UserId)
	if err != nil {
		reply.ErrorCode = -1
		log.WarnTag("gate", "acquire login lock failed uid=%v err=%v", args.UserId, err)
		return err
	}
	defer loginCoord.ReleaseLoginLock(lockHandle)

	localAddr := g.localAddress()
	ownerAddr := loginCoord.GetGateOwner(args.UserId)
	kicked := false

	switch {
	case ownerAddr == "":
		// 首次登录或 meta 已过期：没有需要踢的老连接
	case ownerAddr == localAddr:
		// 老连接在本节点——本地 Offline。phase1 CAS 保证并发安全。
		// F3：Offline(replace=true) 内部已不再固定 sleep 1s，改为有界排空通知队列。
		g.tcpService.Offline(args.UserId, true)
		kicked = true
	default:
		// 老连接在远端——定向 RPC 踢 owner（F3：同步带 ack，返回即远端清理完成）。
		if kickErr := loginCoord.KickRemoteOwner(ownerAddr, args.UserId); kickErr != nil {
			log.WarnTag("gate", "kick remote owner failed uid=%v owner=%v err=%v",
				args.UserId, ownerAddr, kickErr)
			// 不 return——继续尝试本地登录。踢失败大概率是 owner 节点已死（网关重启
			// 残留的脏 owner），登录成功后 SetGateOwner 会覆盖掉死地址；真正的双在线
			// 风险由登录锁互斥兜底。
		}
		kicked = true
	}

	newResumeToken, doErr := g.tcpService.DoLogin(args.UserId, args.TemplateUserId, args.ResumeToken)
	if doErr != nil {
		reply.ErrorCode = -1
		log.WarnTag("gate", "DoLogin failed uid=%v err=%v", args.UserId, doErr)
		// F3（审计：DoLogin 失败不回滚 owner）：老会话已被踢但新会话没立起来——
		// owner meta 若仍指向被踢的老地址则比对清理，避免残留指向已死会话的脏
		// owner 拖慢后续每次登录（对死地址发踢人 RPC 等超时）。
		if kicked && ownerAddr != "" {
			loginCoord.ClearGateOwner(args.UserId, ownerAddr)
		}
		return doErr
	}
	reply.ResumeToken = newResumeToken

	// 登录成功后把 owner 写成本节点。下一次登录时别的节点读到这个值就能精准踢。
	loginCoord.SetGateOwner(args.UserId, localAddr)
	return nil
}

// localAddress 返回本网关实例的 rpcx service address。
// F3：默认透传包级 localGateAddress()；localAddressFn 仅供测试在同一进程内
// 模拟多个网关节点（每个实例各自的地址），生产路径不设置。
func (g *GateService) localAddress() string {
	if g.localAddressFn != nil {
		return g.localAddressFn()
	}
	return localGateAddress()
}

func (g *GateService) Offline(ctx context.Context, args *OfflineReq, reply *OfflineRes) error {
	var ()
	if GetNodeId(ctx) == tgf.NodeId {
		return nil
	}
	g.tcpService.Offline(args.UserId, args.Replace)
	return nil
}

func (g *GateService) ToUser(ctx context.Context, args *ToUserReq, reply *ToUserRes) error {
	// A2-phase3: ITCPService.ToUser 返回 error。广播场景里单个用户的失败不阻断其他用户，
	// 失败只打日志。如果需要返回细粒度失败列表，交给 C1 把 ToUserRes 加 FailedIds 字段。
	for _, userId := range args.UserId {
		if err := g.tcpService.ToUser(userId, args.MessageType, args.Data); err != nil {
			log.DebugTag("gate", "主动推送失败 userId=%v err=%v", userId, err)
			continue
		}
		log.DebugTag("gate", "主动推送 userId=%v msgLen=%v", userId, len(args.Data))
	}
	return nil
}

func GatewayService(tcpBuilder ITCPBuilder) IService {
	service := &GateService{}
	service.tcpBuilder = tcpBuilder
	return service
}

// GatewayServiceWithKCP 构造一个同时启用 TCP/WS 和 KCP 网关的 GateService。
// A8 新增——配合 Server.WithGatewayKCP 使用。
func GatewayServiceWithKCP(tcpBuilder ITCPBuilder, kcpBuilder IKCPBuilder) IService {
	service := &GateService{}
	service.tcpBuilder = tcpBuilder
	service.kcpBuilder = kcpBuilder
	return service
}

var Gate = &Module{Name: "gate", Version: "1.0"}

var (
	UploadUserNodeInfo = &ServiceAPI[*UploadUserNodeInfoReq, *UploadUserNodeInfoRes]{
		ModuleName:  Gate.Name,
		Name:        "UploadUserNodeInfo",
		MessageType: Gate.Name + "." + "UploadUserNodeInfo",
	}

	ToUser = &ServiceAPI[*ToUserReq, *ToUserRes]{
		ModuleName:  Gate.Name,
		Name:        "ToUser",
		MessageType: Gate.Name + "." + "ToUser",
	}

	Login = &ServiceAPI[*LoginReq, *LoginRes]{
		ModuleName:  Gate.Name,
		Name:        "Login",
		MessageType: Gate.Name + "." + "Login",
	}

	Offline = &ServiceAPI[*OfflineReq, *OfflineRes]{
		ModuleName:  Gate.Name,
		Name:        "Offline",
		MessageType: Gate.Name + "." + "Offline",
	}
)

type UploadUserNodeInfoReq struct {
	UserId      string
	NodeId      string
	ServicePath string
}

type UploadUserNodeInfoRes struct {
	ErrorCode int32
}

type ToUserReq struct {
	Data        []byte
	UserId      []string
	MessageType string
}

type ToUserRes struct {
	ErrorCode int32
}

type LoginReq struct {
	UserId         string
	TemplateUserId string
	// Token 是登录凭据（D7 / P0-6 新增）。默认鉴权开启时必填：
	// 由业务登录服务在账号验证后通过 rpc.GenerateLoginToken 签发（默认 HMAC 实现），
	// 或由 Server.WithLoginCheck 注入的自定义校验器解释。
	// 显式 Server.WithoutLoginCheck() 后允许为空（恢复旧的无鉴权行为）。
	Token string
	// ResumeToken 是断线重连令牌（F3 新增，可选）。客户端在重连窗口
	//（默认 30s，见 tcp.go resumeWindow）内重新登录时携带上一次 LoginRes
	// 返回的令牌，网关会把断线期间缓冲的推送按序补发到新连接；
	// 缺省 / 不匹配则按全新会话处理（丢弃缓冲）。
	ResumeToken string
}

type LoginRes struct {
	ErrorCode int32
	// ResumeToken 是本次会话的断线重连令牌（F3 新增）。业务登录服务应把它
	// 转交给客户端保存；客户端断线重连时填入 LoginReq.ResumeToken 即可在
	// 重连窗口内不丢推送。每次登录都会签发新令牌（旧令牌随之失效）。
	ResumeToken string
}

type OfflineReq struct {
	UserId string
	//是否重复登录踢人行为
	Replace bool
}

type OfflineRes struct {
	ErrorCode int32
}

type DefaultArgs struct {
	C string
}

type DefaultReply struct {
	C int32
}

type DefaultBool struct {
	C bool
}

type EmptyReply struct {
}
