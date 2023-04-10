package util

import "reflect"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/4/11
//***************************************************

func ReflectType[T any]() reflect.Type {
	var t T
	v := reflect.ValueOf(t)
	if v.IsNil() {
		v = reflect.New(v.Type().Elem())
	}
	return v.Type().Elem()
}
