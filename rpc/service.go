package rpc

import (
	"github.com/smallnest/rpcx/share"
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
}

type Module struct {
	Name    string
	Version string
}

func (this *Module) Destroy(sub IService) {
	var ()
	log.InfoTag("system", "destroy module=%v version=%v", sub.GetName(), sub.GetVersion())
}

type ServiceAPI[Req, Res any] struct {
	ModuleName string
	Name       string
	Des        string
	args       Req
	reply      Res
}

func (this *ServiceAPI[Req, Res]) New(req Req, res Res) *ServiceAPI[Req, Res] {
	var ()
	return &ServiceAPI[Req, Res]{ModuleName: this.ModuleName, Name: this.Name, args: req, reply: res}
}

func (this *ServiceAPI[Req, Res]) GetResult() Res {
	var ()
	return this.reply
}

func GetUserId(ctx context.Context) string {
	return ctx.Value(share.ReqMetaDataKey).(map[string]string)[tgf.ContextKeyUserId]
}
func GetTemplateUserId(ctx context.Context) string {
	return ctx.Value(share.ReqMetaDataKey).(map[string]string)[tgf.ContextKeyTemplateUserId]
}
