package main

import (
	"strings"
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
	initModule(*common.Modules)
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
		}
	}
}
