package main

import (
	gate "gateserver"
	"github.com/fatih/color"
	"tframework.com/rpc/tcore"
	"tframework.com/rpc/tcore/config"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/server/chat"
	"tframework.com/server/common"
	"tframework.com/server/common/rpc"
	startup "tframework.com/server/startup/internal/interface"
	"tframework.com/server/startup/internal/logic"
)

//***************************************************
//author tim.huang
//2022/8/11
//
//
//***************************************************

var startUpManager startup.IStartUpManager

func main() {
	initModule(tcore.Config.GetModules())
	startUpManager.Start()
}

func init() {
	startUpManager = logic.GetStartupManager()
}

func initModule(modules []*config.ModuleConfig) {
	for _, m := range modules {
		switch m.ModuleName {
		case string(common.Chat):
			s := startUpManager.AddModule(chat.Create(m))
			s.AddOptions(tframework.StartAfter, func(data interface{}) {
				rpc.InitRPCChatService()
			})
		case string(common.Gate):
			s := startUpManager.AddModule(gate.Create(m))
			s.AddOptions(tframework.StartAfter, func(data interface{}) {
				rpc.InitRPCChatService()
			})
		default:
			tcore.Log.WarningS("初始化模块过程中，找不到对应 %v 模块", color.RedString(m.ModuleName))
		}
	}
}
