package main

import (
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/rpc"
	examplechat "github.com/thkhxm/tgf/service/service/chat"
	examplehall "github.com/thkhxm/tgf/service/service/hall"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/27
//***************************************************

// main
//
//	@Description: 启动服务
func main() {
	closeChan := rpc.NewRPCServer().
		//启动chat服务在当前进程
		WithService(&examplechat.ChatService{}).
		//启动hall服务在当前进程
		WithService(&examplehall.HallService{}).
		//启动gate服务在当前进程,gate加载的同时，会加载tcp服务监听用户的请求
		WithGateway("8891").
		//注册rpc服务请求,用于远程调用其他服务(不注册的话,在使用rpc请求的时候也会自动注册)
		//WithServiceClient().
		Run()
	<-closeChan
	log.Info("<-------------------------服务器关闭------------------------->")
}
