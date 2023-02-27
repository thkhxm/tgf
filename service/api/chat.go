package api

import "github.com/thkhxm/tgf/rpc"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/27
//***************************************************

var ChatService = &rpc.Module{Name: "Chat", Version: "1.0"}

var (
	SayHello = rpc.ServiceAPI[string, *ChatServiceSayHelloRPCResponse]{
		ModuleName: ChatService.Name,
		Name:       "RPCSayHello",
	}
)

type ChatServiceSayHelloRPCResponse struct {
	code int32
	Msg  string
}
