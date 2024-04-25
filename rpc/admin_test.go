package rpc_test

import (
	"github.com/thkhxm/tgf/rpc"
	"strconv"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2024/2/20
//***************************************************

func TestServeAdmin(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpc.ServeAdmin(strconv.Itoa(8001))
		})
	}
	select {}
}
