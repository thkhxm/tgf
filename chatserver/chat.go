package chat

import (
	tframework "tframework.com/rpc/tcore/interface"
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
}

func (c *Module) GetModuleName() (moduleName string) {
	return string(common.Chat)
}

func (c *Module) StartUp() {

}

func Create() tframework.ITModule {
	return &Module{}
}
