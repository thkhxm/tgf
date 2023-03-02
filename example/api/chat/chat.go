package chatapi

import "github.com/thkhxm/tgf/rpc"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/27
//***************************************************

var ChatService = &rpc.Module{Name: "Chat", Version: "1.0"}

var (
	SayHello = rpc.ServiceAPI[string, *SayHelloRes]{
		ModuleName: ChatService.Name,
		Name:       "RPCSayHello",
	}
)

type SayHelloRes struct {
	code int32
	Msg  string
}
