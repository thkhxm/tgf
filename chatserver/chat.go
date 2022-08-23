package chat

import (
	"golang.org/x/net/context"
	"tframework.com/rpc/tcore"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/tlog"
	"tframework.com/server/common"
)

//***************************************************
//author tim.huang
//2022/8/11
//
//
//***************************************************

// Module
// @Description: 聊天模块
type Module struct {
	tcore.BaseModule
}

func (c *Module) GetModuleName() (moduleName string) {
	return string(common.Chat)
}

func (c *Module) RPCSayHello(ctx context.Context, args *interface{}, reply *interface{}) error {
	tlog.Debug("rpc chat test")
	return nil
}

func (c *Module) StartUp() {

}

func Create() tframework.ITModule {
	m := &Module{}
	m.AddPlugin(tframework.Log)
	return m
}
