package hallentity

import (
	"fmt"
	"reflect"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/25
//***************************************************

func TestName(t *testing.T) {
	// 获取 User 结构体类型
	userType := reflect.TypeOf(User{})

	// 获取 User 结构体中嵌套的 UserModel 结构体类型
	userModelType := userType.FieldByIndex([]int{0}).Type

	// 遍历 UserModel 结构体类型的所有字段
	for i := 0; i < userModelType.NumField(); i++ {
		field := userModelType.Field(i)
		fmt.Printf("Field %d: %s (%s)\n", i+1, field.Name, field.Type)
	}

}
