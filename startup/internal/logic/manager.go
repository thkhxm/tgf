package logic

import (
	"github.com/fatih/color"
	"os"
	"sync"
	"tframework.com/rpc/tcore"
	tframework "tframework.com/rpc/tcore/interface"
	"tframework.com/server/common/rpc"
	startup "tframework.com/server/startup/internal/interface"
)

//***************************************************
//author tim.huang
//2022/8/13
//
//
//***************************************************

type ModuleMapper []tframework.ITServer

var manager startup.IStartUpManager

// StartupManager
// @Description: 启动器管理
type StartupManager struct {
	moduleMapper ModuleMapper
}

func (s *StartupManager) AddModule(module tframework.ITModule) tframework.ITServer {
	if ser, er := tcore.CreateDefaultTServer(module); er == nil {
		s.moduleMapper = append(s.moduleMapper, ser)
		ser.AddOptions(tframework.StartAfter, func(data interface{}) {
			rpc.RPCFactory.InitFactory()
		}, nil)
		tcore.Log.InfoS("启动器添加新的模块 [%v]", color.RedString(module.GetModuleName()))
		return ser
	} else {
		tcore.Log.WarningS("启动器添加模块异常 [%v]", module.GetModuleName())
		os.Exit(0)
	}
	return nil
}

func (s *StartupManager) Start() {
	for _, server := range s.moduleMapper {
		go func(ser tframework.ITServer) {
			tcore.Log.InfoS("启动器启动模块 [%v] 启动中", color.RedString(ser.GetModule().GetModuleName()))
			ser.StartupServer()
			tcore.Log.InfoS("启动器启动模块 [%v] 启动成功", color.RedString(ser.GetModule().GetModuleName()))
		}(server)
	}
	wait := sync.WaitGroup{}
	wait.Add(1)
	wait.Wait()
}

func init() {
	manager = &StartupManager{moduleMapper: make([]tframework.ITServer, 0)}
}

func GetStartupManager() startup.IStartUpManager {
	return manager
}
