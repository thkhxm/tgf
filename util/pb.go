package util

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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

func ConvertToPB[T protoreflect.ProtoMessage](data []byte) (t T) {
	var ()
	v := reflect.ValueOf(t)
	if v.IsNil() {
		v = reflect.New(v.Type().Elem())
	}
	t = v.Interface().(T)
	proto.Unmarshal(data, t)
	return
}
