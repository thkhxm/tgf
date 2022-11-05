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
// @Description: 聊天模块
type Module struct {
	tcore.BaseModule
}

//***********************    struct_end    ****************************

func (c *Module) GetModuleName() (moduleName string) {
	return string(common.Gate)
}

func (c *Module) StartUp() {

}

func (c *Module) RPCSayHello(ctx context.Context, args *interface{}, reply *interface{}) error {
	tcore.Log.Debug("gate rpc chat test")
	return nil
}

func Create(config *config.ModuleConfig) tframework.ITModule {
	m := &Module{}
	m.AddPlugin(tframework.Log)
	m.AddPlugin(tframework.Consul)
	m.InitStruct(config)
	return m
}
func init() {
}
