package rpc

import (
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

var Chat IChatRPCService

//***********************    var_end    ****************************

//***********************    interface    ****************************

type IChatRPCService interface {
	Say()
}

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

func InitRPCService() {
	tcore.RPCService.RegisterRPCService(new(IChatRPCService), string(common.Chat), "1.0.0")
}

func init() {

}
