package util

import (
	"github.com/thkhxm/tgf/log"
	"sync"
	"testing"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

func TestGo(t *testing.T) {
	type args struct {
		f func()
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
		{"1", args{func() {
			log.Info("ants test %v", 1)
		}}},
		{"2", args{func() {
			log.Info("ants test %v", 2)
		}}},
		{"3", args{func() {
			log.Info("ants test %v", 3)
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Go(tt.args.f)
		})
	}
	w := sync.WaitGroup{}
	w.Add(1)
	w.Wait()
}

func TestInitGoroutinePool(t *testing.T) {
	tests := []struct {
		name string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitGoroutinePool()
		})
	}
}