package rpc

import (
	"github.com/thkhxm/rpcx/share"
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
	GetUserHook() IUserHook
	Destroy(sub IService)
}

type Module struct {
	Name     string
	Version  string
	userHook IUserHook
}

func (m *Module) Destroy(sub IService) {
	var ()
	log.InfoTag("system", "destroy module=%v version=%v", sub.GetName(), sub.GetVersion())
}

func (m *Module) GetUserHook() IUserHook {
	if m.userHook == nil {
		m.userHook = NewUserHook()
	}
	return m.userHook
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
	if resType.Kind() == reflect.Ptr {
		res = resValue.Interface().(Res)
	} else {
		res = resValue.Elem().Interface().(Res)
	}
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
	return ctx.Value(share.ReqMetaDataKey).(map[string]string)[tgf.ContextKeyUserId]
}

func GetNodeId(ctx context.Context) string {
	return ctx.Value(share.ReqMetaDataKey).(map[string]string)[tgf.ContextKeyNodeId]
}

func GetTemplateUserId(ctx context.Context) string {
	return ctx.Value(share.ReqMetaDataKey).(map[string]string)[tgf.ContextKeyTemplateUserId]
}
