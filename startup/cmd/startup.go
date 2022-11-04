package main

import (
	"github.com/fatih/color"
	"tframework.com/rpc/tcore"
	"tframework.com/rpc/tcore/config"
	"tframework.com/server/chat"
	"tframework.com/server/common"
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

func initModule(modules []*config.ModulesConfig) {
	for _, m := range modules {
		switch m.ModuleName {
		case string(common.Chat):
			startUpManager.AddModule(chat.Create(m))
		default:
			tcore.Log.WarningS("初始化模块过程中，找不到对应 %v 模块", color.RedString(m.ModuleName))
		}
	}
}
