package log

import "testing"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

func TestDebug(t *testing.T) {
	type args struct {
		msg    string
		params []interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
		{"1", args{"log test 1 %v %v", []interface{}{"a", "b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Debug(tt.args.msg, tt.args.params...)
		})
	}
}

func TestError(t *testing.T) {
	type args struct {
		msg    string
		params []interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
		{"1", args{"log test 1 %v %v", []interface{}{"a", "b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Error(tt.args.msg, tt.args.params...)
		})
	}
}

func TestInfo(t *testing.T) {
	type args struct {
		msg    string
		params []interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
		{"1", args{"log test 1 %v %v", []interface{}{"a", "b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Info(tt.args.msg, tt.args.params...)
		})
	}
}

func TestWarn(t *testing.T) {
	type args struct {
		msg    string
		params []interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
		{"1", args{"log test 1 %v %v", []interface{}{"a", "b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Warn(tt.args.msg, tt.args.params...)
		})
	}
}

func TestTrace(t *testing.T) {
	type args struct {
		msg    string
		params []interface{}
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
		{"1", args{"log test 1 %v %v", []interface{}{"a", "b"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Trace(tt.args.msg, tt.args.params...)
		})
	}
}
