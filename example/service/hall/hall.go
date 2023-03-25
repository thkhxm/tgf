package examplehall

import (
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/service/api/chat"
	"github.com/thkhxm/tgf/service/api/hall"
	"github.com/thkhxm/tgf/service/api/hall/pb"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/proto"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/2
//***************************************************

type HallService struct {
	rpc.Module
}

func (this *HallService) GetName() string {
	return hallapi.HallService.Name
}

func (this *HallService) GetVersion() string {
	return hallapi.HallService.Version
}

func (this *HallService) Startup() (bool, error) {
	var ()
	return true, nil
}

func (this *HallService) SayHello(ctx context.Context, args *[]byte, reply *[]byte) error {
	var (
		userId = rpc.GetUserId(ctx)
		res    = &chatapi.SayHelloRes{}
		pbReq  = &hallpb.HallSayRequest{}
	)

	if err := proto.Unmarshal(*args, pbReq); err != nil {
		return err
	}

	log.DebugTag("example", "收到用户请求 userId=%v msg=%v", userId, pbReq.Msg)
	//发送rpc到另外一个服务
	//rpc.SendNoReplyRPCMessage(ctx, chatapi.SayHello.New("hello world", res))
	//rpc.SendRPCMessage(ctx, chatapi.SayHello.New("hello world", res))
	//rpc.SendToGate(rpc.NewUserContext("token-testAccount-110"), pbReq)
	log.DebugTag("example", "SayHello userId=%v msg=%v", userId, res.Msg)
	*reply = []byte(res.Msg)
	return nil
}
