package main

import (
	"github.com/smallnest/rpcx/client"
	"golang.org/x/net/context"
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
	d, _ := client.NewConsulDiscovery("/tframework/Chat", "Arith", []string{"127.0.0.1:8500"}, nil)
	// #2
	xclient := client.NewXClient("Chat-RPCSayHello@1.0.0", client.Failtry, client.RandomSelect, d, client.DefaultOption)
	defer xclient.Close()

	// #5
	err := xclient.Call(context.Background(), "RPCSayHello", nil, nil)
	if err != nil {
		tcore.Log.Debug("failed to call: %v", err)
	}
}
func init() {
}
