package tgf

import "github.com/thkhxm/tgf/rpc"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/26
//***************************************************

// gateService
// @Description: 默认网关
type gateService struct {
}

func (this *gateService) GetName() string {
	return GatewayServiceModuleName
}

func (this *gateService) GetVersion() string {
	return "1.0"
}

func GatewayService() rpc.IService {
	service := &gateService{}
	return service
}
