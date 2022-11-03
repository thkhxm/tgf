package test

import (
	"testing"
	"tframework.com/rpc/tcore/internal/plugin"
	"tframework.com/rpc/tcore/tlog"
)

//***************************************************
//author tim.huang
//2022/8/27
//
//
//***************************************************

//***********************    type    ****************************

//***********************    type_end    ****************************

//***********************    var    ****************************

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

type Example2 struct {
	Names []*Ex
	Age   int32
}
type Ex struct {
	Name string
}

type Config struct {
	Example Example2
}

//***********************    struct_end    ****************************

func TestConfig(t *testing.T) {
	val := &Config{}
	plugin.GetData[*Config](val)
	tlog.Debug("test config %v", val.Example.Names[1])
	tlog.Debug("test config %v", val.Example.Age)
}
