package robot

import (
	"github.com/golang/protobuf/proto"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/26
//***************************************************

type CallbackLogic interface {
	Callback(data []byte)
}

type IRobot interface {
	Connect(address string) IRobot
	RegisterCallbackMessage(messageType string, f CallbackLogic) IRobot
	Send(messageType string, v1 proto.Message)
	SendMessage(module, serviceName string, v1 proto.Message)
}
