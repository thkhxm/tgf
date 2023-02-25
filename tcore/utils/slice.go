package utils

import "bytes"

//***************************************************
//author tim.huang
//2022/11/4
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

// ***********************    struct_end    ****************************

// SplitJoinSlice
// @Description: 拼接字符串切片,返回字符串
// @param val
// @param split
// @return _data
func SplitJoinSlice(val []string, split string) (_data string) {
	var buffer bytes.Buffer
	for _, s := range val {
		buffer.WriteString(s)
		buffer.WriteString(split)
	}
	_data = buffer.String()
	//不是空字符,切割最后一个拼接符
	if split != "" {
		_data = _data[0 : len(_data)-1]
	}
	return
}
