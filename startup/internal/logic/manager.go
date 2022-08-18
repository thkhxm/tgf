package logic

import (
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/tlog"
	startup "tframework.com/server/startup/internal/interface"
)

//***************************************************
//author tim.huang
//2022/8/13
//
//
//***************************************************

type ModuleMapper map[string]tframework.ITModule

var manager startup.IStartUpManager

// StartupManager
// @Description: 启动器管理
type StartupManager struct {
	moduleMapper ModuleMapper
}

func (s *StartupManager) AddModule(module tframework.ITModule) {
	s.moduleMapper[module.GetModuleName()] = module
	tlog.ServerLogF("启动器添加新的模块 %v", module.GetModuleName())
}

func init() {
	manager = &StartupManager{moduleMapper: make(map[string]tframework.ITModule)}
}

func GetStartupManager() startup.IStartUpManager {
	return manager
}
