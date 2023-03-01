package util

import (
	"encoding/json"
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
			got, err := StrToAny[StringDemoType](tt.args.a)
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
