package main

import (
	"github.com/fatih/color"
	"strings"
	"tframework.com/rpc/tcore/tlog"
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
	initModule(common.GetModules())
}

func init() {
	startUpManager = logic.GetStartupManager()
}

func initModule(modules string) {
	module := strings.Split(modules, ",")
	for _, m := range module {
		switch m {
		case string(common.Chat):
			startUpManager.AddModule(chat.Create())
		default:
			tlog.WarningS("初始化模块过程中，找不到对应%v模块", color.RedString(m))
		}
	}
}
