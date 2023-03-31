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
	//source := make([]string, 0)
	//n := make([]string, 0)
	//for i := 1; i <= 32; i++ {
	//	source = append(source, strconv.Itoa(i))
	//}
	////{
	////	"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"
	////}
	////1234,5678,9101112,
	////24689101112
	////1357,2
	//sliceSize := len(source)
	//sp := 4
	////group := sliceSize / sp
	//old := strings.Join(source, ",")
	//t.Logf(old)
	//index := 0
	//for i := 0; i < sliceSize; {
	//	if index>sliceSize
	//	n[i] =
	//}
	//msg := strings.Join(source, ",")
	//t.Logf(msg)
}

type StringDemoType struct {
	Name string
}
