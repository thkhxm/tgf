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
	log.Debug("[gate] 修改用户节点信息 userId=%v servicePath=%v nodeId=%v res=%v", args.UserId, args.ServicePath, args.NodeId, reply)
	return nil
}

func GatewayService(tcpBuilder ITCPBuilder) IService {
	service := &GateService{}
	service.tcpBuilder = tcpBuilder
	return service
}

//

var Gate = &Module{Name: "Gate", Version: "1.0"}

var (
	UploadUserNodeInfo = &ServiceAPI[*UploadUserNodeInfoReq, *UploadUserNodeInfoRes]{
		ModuleName: Gate.Name,
		Name:       "UploadUserNodeInfo",
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
