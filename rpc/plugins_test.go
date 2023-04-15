package rpc_test

import (
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/10
//***************************************************

func TestCustomSelector_UpdateServer(t *testing.T) {
	type fields struct {
		moduleName string
	}
	type args struct {
		servers map[string]string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		// TODO: Add test cases.
		{"t1", fields{moduleName: "test"}, args{map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			this := NewCustomSelector(tt.fields.moduleName)
			this.UpdateServer(tt.args.servers)
		})
	}
}
