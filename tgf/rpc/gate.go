package rpc

import (
	"github.com/thkhxm/tgf"
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

// gateService
// @Description: 默认网关
type gateService struct {
}

func (this *gateService) GetName() string {
	return tgf.GatewayServiceModuleName
}

func (this *gateService) GetVersion() string {
	return "1.0"
}

func (this *gateService) S(ctx context.Context, args *interface{}, reply *interface{}) error {
	var ()
	return nil
}

func GatewayService() IService {
	service := &gateService{}
	return service
}
