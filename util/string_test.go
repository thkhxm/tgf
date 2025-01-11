package util_test

import (
	"github.com/bytedance/sonic"
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
	arg, _ := sonic.Marshal(ddd)
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
	//s, _ := util.util.StrToAny[float64]("0.38")
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
func TestStrToAnyBool(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    bool
		wantErr bool
	}{
		{"valid true", "true", true, false},
		{"valid false", "false", false, false},
		{"invalid bool", "notabool", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.StrToAny[bool](tt.arg)
			if (err != nil) != tt.wantErr {
				t.Errorf("StrToAny() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("StrToAny() got = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestStrToAnyInt(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    int
		wantErr bool
	}{
		{"valid int", "123", 123, false},
		{"invalid int", "notanint", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.StrToAny[int](tt.arg)
			if (err != nil) != tt.wantErr {
				t.Errorf("StrToAny() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("StrToAny() got = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestStrToAnyFloat64(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    float64
		wantErr bool
	}{
		{"valid float", "123.456", 123.456, false},
		{"invalid float", "notafloat", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.StrToAny[float64](tt.arg)
			if (err != nil) != tt.wantErr {
				t.Errorf("StrToAny() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("StrToAny() got = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestStrToAnyFloat32(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    float32
		wantErr bool
	}{
		{"valid float", "123.456", 123.456, false},
		{"invalid float", "notafloat", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.StrToAny[float32](tt.arg)
			if (err != nil) != tt.wantErr {
				t.Errorf("StrToAny() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("StrToAny() got = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestStrToAnyString(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    string
		wantErr bool
	}{
		{"valid string", "hello", "hello", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.StrToAny[string](tt.arg)
			if (err != nil) != tt.wantErr {
				t.Errorf("StrToAny() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("StrToAny() got = %v, want %v", got, tt.want)
			}
		})
	}
}
func TestStrToAnyStruct(t *testing.T) {
	type MyStruct struct {
		Name string
	}
	tests := []struct {
		name    string
		arg     string
		want    MyStruct
		wantErr bool
	}{
		{"valid struct", "{\"Name\":\"test\"}", MyStruct{Name: "test"}, false},
		{"invalid struct", "notastruct", MyStruct{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := util.StrToAny[MyStruct](tt.arg)
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

type StringDemoType struct {
	Name string
}
