package gate

import (
	"golang.org/x/net/context"
	"tframework.com/rpc/tcore"
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
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

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

// Module
// @Description:网关模块
type Module struct {
	tcore.BaseModule
}

//***********************    struct_end    ****************************

func (c *Module) GetModuleName() (moduleName string) {
	return string(common.Gate)
}

func (c *Module) StartUp() {

}

func (c *Module) RPCSendToPlayer(ctx context.Context, args *interface{}, reply *interface{}) error {
	tcore.Log.Debug("gate rpc send to player")
	return nil
}

func Create(config *config.ModuleConfig) tframework.ITModule {
	m := &Module{}
	m.AddPlugin(tframework.Log)
	m.AddPlugin(tframework.Consul)
	m.InitStruct(config)

	//go func() {
	//	time.Sleep(time.Second * 5)
	//	sample := make(map[string]*rpc.RPCSampleData)
	//	cars := make(map[string]string)
	//	cars["ad"] = "aodi"
	//	sample["demo"] = &rpc.RPCSampleData{
	//		Car:   cars,
	//		Money: 99999,
	//	}
	//	req := &rpc.RPCSayHelloRequest{
	//		Name:       "tim",
	//		Friends:    []int32{1, 2, 3},
	//		SampleData: sample,
	//	}
	//
	//	for i := 0; i < 10; i++ {
	//		response := &rpc.RPCSayHelloResponse{
	//			Code:    0,
	//			Message: "123",
	//			Data:    new(rpc.RPCResponseData),
	//		}
	//		callBack, _ := tcore.RPCService.SendOne(rpc.IRPCChatService.RPCSayHello, int32(tframework.Default), req, response)
	//		go func(cb tframework.IRPCCallBack) {
	//			data := cb.Done()
	//			if data != nil {
	//				tcore.Log.InfoS("-------->%v", data.(*rpc.RPCSayHelloResponse).Data)
	//			}
	//		}(callBack)
	//
	//	}
	//}()
	return m
}
func init() {
}
