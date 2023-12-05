package rpc

import (
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
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
// @Description: 默认网关
type GateService struct {
	Module
	tcpBuilder ITCPBuilder
	tcpService ITCPService
}

func (this *GateService) GetName() string {
	return tgf.GatewayServiceModuleName
}

func (this *GateService) GetVersion() string {
	return "1.0"
}

func (this *GateService) Startup() (bool, error) {
	var ()
	this.tcpService = newDefaultTCPServer(this.tcpBuilder)
	this.tcpService.Run()
	return true, nil
}

func (this *GateService) UploadUserNodeInfo(ctx context.Context, args *UploadUserNodeInfoReq, reply *UploadUserNodeInfoRes) error {
	var ()
	if ok := this.tcpService.UpdateUserNodeInfo(args.UserId, args.ServicePath, args.NodeId); !ok {
		reply.ErrorCode = -1
	}
	log.DebugTag("gate", "修改用户节点信息 userId=%v servicePath=%v nodeId=%v res=%v", args.UserId, args.ServicePath, args.NodeId, reply)
	return nil
}

func (this *GateService) Login(ctx context.Context, args *LoginReq, reply *LoginRes) error {
	var ()
	//踢人,重复登录的
	if !this.tcpService.Offline(args.UserId, true) {
		BorderRPCMessage(ctx, Offline.New(&OfflineReq{UserId: args.UserId}, new(OfflineRes)))
	}
	err := this.tcpService.DoLogin(args.UserId, args.TemplateUserId)
	return err
}

func (this *GateService) Offline(ctx context.Context, args *OfflineReq, reply *OfflineRes) error {
	var ()
	if GetNodeId(ctx) == tgf.NodeId {
		return nil
	}
	this.tcpService.Offline(args.UserId, args.Replace)
	return nil
}

func (this *GateService) ToUser(ctx context.Context, args *ToUserReq, reply *ToUserRes) error {
	var ()
	this.tcpService.ToUser(args.UserId, args.MessageType, args.Data)
	log.DebugTag("gate", "主动推送 userId=%v msgLen=%v", args.UserId, args.UserId, len(args.Data))
	return nil
}

func GatewayService(tcpBuilder ITCPBuilder) IService {
	service := &GateService{}
	service.tcpBuilder = tcpBuilder
	return service
}

type IUserHook interface {
	GetLoginHooks() []*ServiceAPI[*DefaultArgs, *EmptyReply]
	GetOfflineHooks() []*ServiceAPI[*DefaultArgs, *EmptyReply]
	AddLoginHook(hook *ServiceAPI[*DefaultArgs, *EmptyReply]) IUserHook
	AddOfflineHook(hook *ServiceAPI[*DefaultArgs, *EmptyReply]) IUserHook
}

type UserHook struct {
	loginHooks   []*ServiceAPI[*DefaultArgs, *EmptyReply]
	offlineHooks []*ServiceAPI[*DefaultArgs, *EmptyReply]
}

func (u *UserHook) AddLoginHook(hook *ServiceAPI[*DefaultArgs, *EmptyReply]) IUserHook {
	u.loginHooks = append(u.loginHooks, hook)
	return u
}

func (u *UserHook) AddOfflineHook(hook *ServiceAPI[*DefaultArgs, *EmptyReply]) IUserHook {
	u.offlineHooks = append(u.offlineHooks, hook)
	return u
}

func (u *UserHook) GetLoginHooks() []*ServiceAPI[*DefaultArgs, *EmptyReply] {
	return u.loginHooks
}

func (u *UserHook) GetOfflineHooks() []*ServiceAPI[*DefaultArgs, *EmptyReply] {
	return u.offlineHooks
}

func NewUserHook() IUserHook {
	return &UserHook{
		loginHooks:   make([]*ServiceAPI[*DefaultArgs, *EmptyReply], 0),
		offlineHooks: make([]*ServiceAPI[*DefaultArgs, *EmptyReply], 0),
	}
}

var Gate = &Module{Name: "Gate", Version: "1.0"}

var (
	UploadUserNodeInfo = &ServiceAPI[*UploadUserNodeInfoReq, *UploadUserNodeInfoRes]{
		ModuleName: Gate.Name,
		Name:       "UploadUserNodeInfo",
	}

	ToUser = &ServiceAPI[*ToUserReq, *ToUserRes]{
		ModuleName: Gate.Name,
		Name:       "ToUser",
	}

	Login = &ServiceAPI[*LoginReq, *LoginRes]{
		ModuleName: Gate.Name,
		Name:       "Login",
	}

	Offline = &ServiceAPI[*OfflineReq, *OfflineRes]{
		ModuleName: Gate.Name,
		Name:       "Offline",
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
	UserId      string
	MessageType string
}

type ToUserRes struct {
	ErrorCode int32
}

type LoginReq struct {
	UserId         string
	TemplateUserId string
}

type LoginRes struct {
	ErrorCode int32
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
