package rpc

import (
	"github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/share"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"golang.org/x/net/context"
	"reflect"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

// IService
//
//	@Description: 逻辑服务接口
type IService interface {
	GetName() string
	GetVersion() string
	Startup() (bool, error)
	Destroy(sub IService)
	GetLogicSyncMethod() []string
}

type Module struct {
	Name            string
	Version         string
	State           client.ConsulServerState
	userLoginHook   []loginHook
	userOfflineHook []offlineHook
}

func (m *Module) GetName() string {
	return m.Name
}

func (m *Module) GetVersion() string {
	return m.Version
}

func (m *Module) Destroy(sub IService) {
	var ()
	log.InfoTag("system", "destroy module=%v version=%v", sub.GetName(), sub.GetVersion())
}

func (m *Module) GetLogicSyncMethod() []string {
	return nil
}

func (m *Module) StateHandler(ctx context.Context, args *client.ConsulServerState, reply *string) (err error) {
	m.State = *args
	log.InfoTag("system", "update module state %s to %s module=%v version=%v", m.State, args, m.Name, m.Version)
	return
}

func (m *Module) AddUserLoginHook(hook loginHook) {
	m.userLoginHook = append(m.userLoginHook, hook)
}
func (m *Module) AddUserOfflineHook(hook offlineHook) {
	m.userOfflineHook = append(m.userOfflineHook, hook)
}

func (m *Module) OfflineHook(ctx context.Context, args *OfflineReq, reply *EmptyReply) (err error) {
	if len(m.userOfflineHook) == 0 {
		return
	}
	for _, hook := range m.userOfflineHook {
		err = hook(ctx, args.UserId, args.Replace)
		if err != nil {
			return
		}
	}
	return
}

func (m *Module) LoginHook(ctx context.Context, args *DefaultArgs, reply *EmptyReply) (err error) {
	if len(m.userLoginHook) == 0 {
		return
	}
	for _, hook := range m.userLoginHook {
		err = hook(ctx, args.C)
		if err != nil {
			return
		}
	}
	return
}

type ServiceAPI[Req, Res any] struct {
	ModuleName  string
	Name        string
	MessageType string
	Des         string
	args        Req
	reply       Res
}

func (s *ServiceAPI[Req, Res]) New(req Req, res Res) *ServiceAPI[Req, Res] {
	var ()
	return &ServiceAPI[Req, Res]{ModuleName: s.ModuleName, Name: s.Name, args: req, reply: res, MessageType: s.MessageType}
}

func (s *ServiceAPI[Req, Res]) NewRPC(req Req) *ServiceAPI[Req, Res] {
	var ()
	var res Res
	resType := reflect.TypeOf((*Res)(nil)).Elem() // 获取Res的类型
	resValue := reflect.New(resType)              // 创建Res的新实例

	// 如果Res是一个指针类型，我们需要通过.Elem()获取其指向的值
	//if resType.Kind() == reflect.Ptr {
	//	res = resValue.Interface().(Res)
	//} else {
	res = resValue.Elem().Interface().(Res)
	//}
	return &ServiceAPI[Req, Res]{ModuleName: s.ModuleName, Name: s.Name, args: req, reply: res, MessageType: s.MessageType}
}

func (s *ServiceAPI[Req, Res]) NewEmpty() *ServiceAPI[Req, Res] {
	var ()
	var req Req
	var res Res
	return &ServiceAPI[Req, Res]{ModuleName: s.ModuleName, Name: s.Name, args: req, reply: res, MessageType: s.MessageType}
}

func (s *ServiceAPI[Req, Res]) GetResult() Res {
	var ()
	return s.reply
}

func GetUserId(ctx context.Context) string {
	if ct, ok := ctx.(*share.Context); ok {
		return ct.GetReqMetaDataByKey(tgf.ContextKeyUserId)
	}
	return ""
}

func GetNodeId(ctx context.Context) string {
	if ct, ok := ctx.(*share.Context); ok {
		return ct.GetReqMetaDataByKey(tgf.ContextKeyNodeId)
	}
	return ""
}

func GetTemplateUserId(ctx context.Context) string {
	if ct, ok := ctx.(*share.Context); ok {
		return ct.GetReqMetaDataByKey(tgf.ContextKeyTemplateUserId)
	}
	return ""
}
