package tserver

import "tframework.com/rpc/tcore"

//***************************************************
//author tim.huang
//2022/8/10
//
//
//***************************************************

// TServer
// @Description:
type TServer struct {
	startDetails map[tcore.TServerStatus]StartDetail
}

// StartDetail
// @Description: 启动详情
// TODO: 名字需要修改一下，跟他的业务不太符合
type StartDetail struct {
	status  tcore.TServerStatus
	options []tcore.ITServerOptions
}

func (s *TServer) AddOptions(status tcore.TServerStatus, options ...tcore.ITServerOptions) tcore.ITServer {
	return s
}

func (s *TServer) StartupServer() {

}
