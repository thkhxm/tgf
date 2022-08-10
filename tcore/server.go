package tcore

import (
	"tframework.com/rpc/tcore/interface"
	"tframework.com/rpc/tcore/internal/server"
)

//***************************************************
//author tim.huang
//2022/8/10
//
//
//***************************************************

// CreateDefaultTServer
// @Description: 创建一个新的服务
// @return *ITServer
// @return error
func CreateDefaultTServer(module tframework.ITModule) (tframework.ITServer, error) {
	server := &tserver.TServer[tframework.ITModule]{}
	server.SetModule(module)
	return server, nil
}
