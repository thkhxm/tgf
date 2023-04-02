package util_test

import (
	"encoding/json"
	"github.com/thkhxm/tgf/util"
	"reflect"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/24
//***************************************************

func TestStrToAny(t *testing.T) {
	type args struct {
		a string
	}
	type testCase[T any] struct {
		name    string
		args    args
		want    T
		wantErr bool
	}
	ddd := &StringDemoType{Name: "333"}
	arg, _ := json.Marshal(ddd)
	tests := []testCase[StringDemoType]{
		{name: "1", args: args{string(arg)}, want: *ddd, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.StrToAny[StringDemoType](tt.args.a)
			if (err != nil) != tt.wantErr {
				t.Errorf("StrToAny() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("StrToAny() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecover(t *testing.T) {
	//s, _ := util.StrToAny[float64]("0.38")
	//t.Logf("-->%v", s)
	//var (
	//	a int            = 1
	//	b int32          = 1
	//	c int64          = 1
	//	d float32        = 0.38
	//	e float64        = 0.38
	//	f bool           = true
	//	g StringDemoType = *new(StringDemoType)
	//)
	//g.Name = "tim"
	//a0, _ := util.AnyToStr(a)
	//a1, _ := util.AnyToStr(b)
	//a2, _ := util.AnyToStr(c)
	//a3, _ := util.AnyToStr(d)
	//a4, _ := util.AnyToStr(e)
	//a5, _ := util.AnyToStr(f)
	//a6, _ := util.AnyToStr(g)
	//t.Log("--->", a0)
	//t.Log("--->", a1)
	//t.Log("--->", a2)
	//t.Log("--->", a3)
	//t.Log("--->", a4)
	//t.Log("--->", a5)
	//t.Log("--->", a6)
	dd, _ := util.StrToAny[*StringDemoType]("{\"Name\":\"tim\"}")

	t.Log("--->", dd)
}

type StringDemoType struct {
	Name string
}
