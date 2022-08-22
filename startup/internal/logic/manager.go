package logic

import (
	"github.com/fatih/color"
	"os"
	"tframework.com/rpc/tcore"
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

type ModuleMapper map[string]tframework.ITServer

var manager startup.IStartUpManager

// StartupManager
// @Description: 启动器管理
type StartupManager struct {
	moduleMapper ModuleMapper
}

func (s *StartupManager) AddModule(module tframework.ITModule) {
	if ser, er := tcore.CreateDefaultTServer(module); er == nil {
		s.moduleMapper[module.GetModuleName()] = ser
		tlog.InfoS("启动器添加新的模块 [%v]", color.RedString(module.GetModuleName()))
	} else {
		tlog.WarningS("启动器添加模块异常 [%v]", module.GetModuleName())
		os.Exit(0)
	}

}

func (s *StartupManager) Start() {
	for moduleName, server := range s.moduleMapper {
		tlog.InfoS("启动器启动模块 [%v] 启动中", color.RedString(moduleName))
		server.StartupServer()
		tlog.InfoS("启动器启动模块 [%v] 启动成功", color.RedString(moduleName))
	}
}

func init() {
	manager = &StartupManager{moduleMapper: make(map[string]tframework.ITServer)}
}

func GetStartupManager() startup.IStartUpManager {
	return manager
}
