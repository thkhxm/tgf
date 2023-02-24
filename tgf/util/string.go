package util

import (
	"encoding/json"
	"fmt"
	"strconv"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/24
//***************************************************

// StrToAny
// @Description: string转任意类型
// @param a
// @return T
// @return error
func StrToAny[T any](a string) (T, error) {
	var t T
	switch any(t).(type) {
	case bool:
		v, err := strconv.ParseBool(a)
		if err != nil {
			return t, err
		}
		t = any(v).(T)
	case int32:
		v, err := strconv.ParseInt(a, 10, 32)
		if err != nil {
			return t, err
		}
		t = any(v).(T)
	case int:
		v, err := strconv.ParseInt(a, 10, 32)
		if err != nil {
			return t, err
		}
		t = any(v).(T)
	case int64:
		v, err := strconv.ParseInt(a, 10, 64)
		if err != nil {
			return t, err
		}
		t = any(v).(T)
	case float32:
		v, err := strconv.ParseFloat(a, 32)
		if err != nil {
			return t, err
		}
		t = any(v).(T)
	case float64:
		v, err := strconv.ParseFloat(a, 64)
		if err != nil {
			return t, err
		}
		t = any(v).(T)
	case string:
		v := a
		t = any(v).(T)
	case interface{}:
		json.Unmarshal([]byte(a), &t)
	default:
		return t, fmt.Errorf("the type %T is not supported", t)
	}
	return t, nil
}
