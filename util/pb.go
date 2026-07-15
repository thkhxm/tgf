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
	t, _ = ConvertToPBWithError[T](data)
	return
}

// ConvertToPBWithError decodes protobuf data and reports malformed input.
// ConvertToPB remains as the compatibility helper for callers that cannot handle an error.
func ConvertToPBWithError[T protoreflect.ProtoMessage](data []byte) (t T, err error) {
	var ()
	v := reflect.ValueOf(t)
	if v.IsNil() {
		v = reflect.New(v.Type().Elem())
	}
	t = v.Interface().(T)
	err = proto.Unmarshal(data, t)
	return
}
