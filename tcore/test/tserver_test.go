package test

import (
	"github.com/rpcxio/rpcx-consul/client"
	client2 "github.com/smallnest/rpcx/client"
	"golang.org/x/net/context"
	"testing"
	"tframework.com/rpc/tcore"
	"time"
)

type TestServer struct {
	tcore.BaseModule `json:"tcore_._base_module"`
	DD               int `cnf:"123"`
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

func TestTag(t *testing.T) {
	// #1
	d, _ := client.NewConsulDiscovery("/tframework/Chat", "Chat@1.0.0", []string{"127.0.0.1:8500"}, nil)
	// #2
	xclient := client2.NewXClient("Chat@1.0.0", client2.Failtry, client2.RandomSelect, d, client2.DefaultOption)
	defer xclient.Close()
	time.Sleep(time.Second * 10)
	for i := 0; i < 10; i++ {
		// #5
		err := xclient.Call(context.Background(), "RPCSayHello", nil, nil)
		if err != nil {
			tcore.Log.Debug("failed to call: %v", err)
		}
	}
}
