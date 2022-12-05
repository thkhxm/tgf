package gate

import (
	"golang.org/x/net/context"
	"tframework.com/rpc/tcore"
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/server/common"
	"tframework.com/server/common/rpc"
	"time"
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
	go func() {
		time.Sleep(time.Second * 5)
		arg := 5
		tcore.RPCService.Send(rpc.IRPCChatService.RPCSayHello, int32(tframework.Default), arg, nil)
	}()
	return m
}
func init() {
}
