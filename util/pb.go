package util

import (
	"github.com/golang/protobuf/proto"
	"google.golang.org/protobuf/runtime/protoiface"
	"reflect"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/17
//***************************************************

func ConvertToPB[T protoiface.MessageV1](data []byte) (t T) {
	var ()
	v := reflect.ValueOf(t)
	if v.IsNil() {
		v = reflect.New(v.Type().Elem())
	}
	t = v.Interface().(T)
	proto.Unmarshal(data, t)
	return
}
