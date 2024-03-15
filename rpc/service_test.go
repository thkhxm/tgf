package rpc

import (
	"testing"
)

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ群 7400585
// author tim.huang<thkhxm@gmail.com>
// @Description
// 2024/2/23
// ***************************************************
type rpcTestData struct {
	DD string
}

func TestServiceAPI_NewRPC(t *testing.T) {
	Login.NewRPC(&LoginReq{})
}
