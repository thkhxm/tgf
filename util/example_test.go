package util_test

import (
	"fmt"
	"github.com/thkhxm/tgf/util"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/6
//***************************************************

type StrToAnyStruct struct {
	Name string
	Age  int32
}

func ExampleStrToAny() {
	//to int
	if numInt, err := util.StrToAny[int]("1024"); err == nil {
		fmt.Println("numInt:", numInt)
	}

	//to interface{}
	if structData, err2 := util.StrToAny[StrToAnyStruct]("{\"Name\":\"tim\",\"Age\":5}"); err2 == nil {
		fmt.Println("json data:", structData.Name)
	}
	// Output:
	// numInt: 1024
	// json data: tim
}
