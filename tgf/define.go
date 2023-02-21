package tgf

import "os"

//***************************************************
//author tim.huang
//2023/2/21
//
//
//***************************************************

// ***********************    type    ****************************
type RuntimeModule string

var (
	RuntimeModule_Dev     RuntimeModule = "dev"
	RuntimeModule_Test                  = "test"
	RuntimeModule_Release               = "release"
)

//***********************    type_end    ****************************

//***********************    var    ****************************

var Module RuntimeModule = RuntimeModule_Dev

//***********************    var_end    ****************************

//***********************    interface    ****************************

//***********************    interface_end    ****************************

//***********************    struct    ****************************

//***********************    struct_end    ****************************

func init() {
	os.Getenv("RuntimeModule")
}
