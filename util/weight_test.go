package util_test

import (
	"github.com/thkhxm/tgf/util"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/11/29
//***************************************************

func Test_weightOperation_Roll(t *testing.T) {
	builder := util.NewWeightBuilder[int32]().Seed(1001)
	builder.AddWeight(10, 10, 1)
	builder.AddWeight(20, 10, 2)
	builder.AddWeight(30, 1, 3)
	builder.AddWeight(40, 10, 4)
	builder.AddWeight(50, 10, 5)
	w := builder.Build()
	for i := 0; i < 5; i++ {
		t.Logf("roll number : %d", w.Roll())
	}

	//=== RUN   Test_weightOperation_Roll
	//weight_test.go:26: roll number : &{50 5 9}
	//weight_test.go:26: roll number : &{30 3 0}
	//weight_test.go:26: roll number : &{50 5 8}
	//weight_test.go:26: roll number : &{40 4 9}
	//weight_test.go:26: roll number : &{40 4 8}
	//--- PASS: Test_weightOperation_Roll (0.00s)
}
