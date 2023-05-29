package rpc_test

import (
	"github.com/thkhxm/tgf/rpc"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/5/23
//***************************************************

func TestStartServer(t *testing.T) {
	tests := []struct {
		name string
	}{
		// TODO: Add test cases.
		{"123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpc.StartServer()
		})
	}
	select {}
}

func TestStartClient(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"321"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpc.StartClient()
		})
	}
	select {}
}
