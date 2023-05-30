package robot_test

import (
	"github.com/thkhxm/tgf/robot"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/5/30
//***************************************************

func TestNewRobotWs(t *testing.T) {
	type args struct {
		path string
	}
	tests := []struct {
		name string
		args args
		want robot.IRobot
	}{
		{"1", args{path: "/tgf"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := robot.NewRobotWs(tt.args.path)
			got.Connect("127.0.0.1:8443")
		})
	}
	select {}
}
