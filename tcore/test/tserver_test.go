package test

import (
	"reflect"
	"testing"
	"tframework.com/rpc/tcore"
)

type TestServer struct {
	tcore.BaseModule `json:"tcore_._base_module"`
	DD               int `cnf:"123"`
}

func (receiver *TestServer) GetModuleName() (moduleName string) {
	return "test"
}

func (receiver *TestServer) RPCFindBooks() {

}

func (receiver *TestServer) RPcFindApple() {

}
func (receiver *TestServer) SayRed() {

}

func TestTag(t *testing.T) {
	ts := TestServer{}
	tt := reflect.TypeOf(ts)
	st, _ := tt.FieldByName("DD")
	t.Log(st.Tag.Get("cnf"))
}
