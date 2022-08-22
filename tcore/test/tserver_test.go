package test

import "tframework.com/rpc/tcore"

type TestServer struct {
	tcore.BaseModule
}

func (receiver *TestServer) GetModuleName() (moduleName string) {
	return "test"
}

func (receiver *TestServer) RPCFindBooks() {

}

func (receiver *TestServer) RPcFindApple() {

}
func (receiver *TestServer) SayRed() {

}
