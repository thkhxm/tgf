package example

import (
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc"
	"github.com/thkhxm/tgf/service/api/chat"
	"github.com/thkhxm/tgf/service/api/hall"
	hallpb "github.com/thkhxm/tgf/service/api/hall/pb"
	"golang.org/x/net/context"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/27
//***************************************************

//用户通过网关服,请求HallService的SayHello函数.
////HallService服务中的SayHello函数,将消息通过rpc请求抵达ChatService服务
////ChatService通过RPCSayHello函数,重新拼装消息Message,并返回最终字符串
////HallService收到rpc的返回,将最终结果返回用户所在的网关服
////网关服接收到HallService的响应,返回响应结果到用户

type ChatService struct {
	rpc.Module
}

func (this *ChatService) RPCSayHello(ctx context.Context, req *string, response *chatapi.SayHelloRes) error {
	var (
		userId = rpc.GetUserId(ctx)
		msg    = *req
	)
	log.Debug("[example] RPCSayHello userId=%v ,msg=%v", userId, msg)
	response.Msg = fmt.Sprintf("%v say %v", userId, msg)
	return nil
}

func (this *ChatService) GetName() string {
	return chatapi.ChatService.Name
}

func (this *ChatService) GetVersion() string {
	return chatapi.ChatService.Version
}

type HallService struct {
	rpc.Module
}

func (this *HallService) GetName() string {
	return hallapi.HallService.Name
}

func (this *HallService) GetVersion() string {
	return hallapi.HallService.Version
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

	log.Debug("[example] 收到用户请求 userId=%v msg=%v", userId, pbReq.Msg)
	rpc.SendRPCMessage(ctx, chatapi.SayHello.New("hello world", res))
	log.Debug("[example] SayHello userId=%v msg=%v", userId, res.Msg)
	*reply = []byte(res.Msg)
	return nil
}
