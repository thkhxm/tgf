package main

import (
	client2 "github.com/rpcxio/rpcx-consul/client"
	"github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/server"
	"golang.org/x/net/context"
	"sync"
	"testing"
	"tframework.com/rpc/tcore"
)

// ***************************************************
// author tim.huang
// 2022/8/23
//
// ***************************************************
var add = "127.0.0.1:8081"

func TestRPC(t *testing.T) {
	// #1
	d, _ := client2.NewConsulDiscovery("/tframework/Chat", "Chat", []string{"127.0.0.1:8500"}, nil)
	// #2
	xclient := client.NewXClient("Chat-RPCSayHello@1.0.0", client.Failtry, client.RandomSelect, d, client.DefaultOption)
	defer xclient.Close()

	// #5
	err := xclient.Call(context.Background(), "RPCSayHello", nil, nil)
	if err != nil {
		tcore.Log.Debug("failed to call: %v", err)
	}
	w := sync.WaitGroup{}
	w.Wait()
}

type DemoServer struct {
}

func TestServerRegister(t *testing.T) {
	s := server.NewServer()
	s.RegisterName("Arith", new(DemoServer), "")
	s.Serve("tcp", "localhost:8083")
}
func init() {
}
