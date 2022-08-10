package tcore

import "tframework.com/rpc/tcore/internal/server"

//***************************************************
//author tim.huang
//2022/8/10
//
//
//***************************************************

// CreateTServer 服务发现
// @Description: 创建一个新的服务
// @return *ITServer
// @return error
func CreateTServer() (ITServer, error) {
	return &tserver.TServer{}, nil
}
