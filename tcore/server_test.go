package tcore

import (
	"reflect"
	"testing"
	tframework "tframework.com/rpc/tcore/interface"
)

//***************************************************
//author tim.huang
//2022/8/11
//
//
//***************************************************

func TestCreateDefaultTServer(t *testing.T) {
	type args struct {
		module tframework.ITModule
	}
	tests := []struct {
		name    string
		args    args
		want    tframework.ITServer
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CreateDefaultTServer(tt.args.module)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateDefaultTServer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CreateDefaultTServer() got = %v, want %v", got, tt.want)
			}
		})
	}
}
