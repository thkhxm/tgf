package startup

import tframework "tframework.com/rpc/tcore/interface"

// ***************************************************
// author tim.huang
// 2022/8/18
//
// ***************************************************
type IStartUpManager interface {
	AddModule(module tframework.ITModule) tframework.ITServer
	Start()
}
