//go:build integration
// +build integration

package rpc_test

import "github.com/thkhxm/tgf/v2/rpc"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/8/16
//***************************************************

// ExampleNewRPCServer
//
//	@Description:  rpc server
func ExampleNewRPCServer() {
	// rpc server
	r := rpc.NewRPCServer().WithService(new(rpc.GateService)).Run()
	<-r
}
