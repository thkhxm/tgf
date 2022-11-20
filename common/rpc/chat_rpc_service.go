package rpc

import (
	"golang.org/x/net/context"
	"tframework.com/rpc/tcore"
	"tframework.com/server/common"
)

//***************************************************
//author tim.huang
//2022/11/5
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

var Chat IRPCChatService

//***********************    var_end    ****************************

//***********************    interface    ****************************

type IRPCChatService interface {
	RPCSayHello(ctx context.Context, args *interface{}, reply *interface{})
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

type chatServiceImpl struct {
}

func (c chatServiceImpl) RPCSayHello(ctx context.Context, args *interface{}, reply *interface{}) {
	//TODO implement me
	panic("implement me")
}

func InitRPCChatService() {
	Chat = new(chatServiceImpl)
	tcore.RPCService.RegisterRPCService(Chat, string(common.Chat), "1.0.0")
}

func init() {

}
