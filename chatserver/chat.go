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

// ChatModule
// @Description: 聊天模块
type ChatModule struct {
}

func (c ChatModule) GetModuleName() (moduleName string) {
	return string(common.Chat)
}

func (c ChatModule) Startup() {

}

func Create() tframework.ITModule {
	return &ChatModule{}
}
