package util

import (
	"encoding/json"
	"fmt"
	"strconv"
	"unsafe"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/24
//***************************************************

// StrToAny
//
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
		t = *(*T)(unsafe.Pointer(&v))
	case int:
		v, err := strconv.ParseInt(a, 10, 32)
		if err != nil {
			return t, err
		}
		t = *(*T)(unsafe.Pointer(&v))
	case int64:
		v, err := strconv.ParseInt(a, 10, 64)
		if err != nil {
			return t, err
		}
		t = any(v).(T)
	case float32:
		v, err := strconv.ParseFloat(a, 64)
		if err != nil {
			return t, err
		}
		t = *(*T)(unsafe.Pointer(&v))
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
		err := json.Unmarshal([]byte(a), &t)
		if err != nil {
			return t, err
		}
	default:
		return t, fmt.Errorf("the type %T is not supported", t)
	}
	return t, nil
}

// AnyToStr
//
//	@Description: 任意数据转换成字符串，默认结构化数据使用json序列化
//	@param a
//	@return string
//	@return error
func AnyToStr(a interface{}) (string, error) {
	switch a.(type) {
	case bool:
		return strconv.FormatBool(a.(bool)), nil
	case int32:
		return strconv.FormatInt(int64(a.(int32)), 10), nil
	case int:
		return strconv.FormatInt(int64(a.(int)), 10), nil
	case int64:
		return strconv.FormatInt(a.(int64), 10), nil
	case float32:
		return strconv.FormatFloat(float64(a.(float32)), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(a.(float64), 'f', -1, 64), nil
	case string:
		return a.(string), nil
	case interface{}:
		js, _ := json.Marshal(a)
		return ConvertStringByByteSlice(js), nil
	default:
		return "", fmt.Errorf("the type %T is not supported", a)
	}
}

// ConvertStringByByteSlice
// @Description: 字节转字符串
// @param bytes
// @return string
//
//go:inline
func ConvertStringByByteSlice(bytes []byte) string {
	return *(*string)(unsafe.Pointer(&bytes))
}
