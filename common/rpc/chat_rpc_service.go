package rpc

import (
	"golang.org/x/net/context"
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
	RPCSayHello(ctx context.Context, args *RPCSayHelloRequest, reply *RPCSayHelloResponse) error
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

type RPCSayHelloRequest struct {
	Name       string
	Friends    []int32
	SampleData map[string]*RPCSampleData
}

type RPCSayHelloResponse struct {
	Code    int32
	Message string
	Data    *RPCResponseData
}
type RPCResponseData struct {
	Friends []int32
}
type RPCSampleData struct {
	Car   map[string]string
	Money int64
}

//***********************    struct_end    ****************************

type chatServiceImpl struct {
}

func (c chatServiceImpl) RPCSayHello(ctx context.Context, args *RPCSayHelloRequest, reply *RPCSayHelloResponse) error {
	//TODO implement me
	panic("implement me")
}
