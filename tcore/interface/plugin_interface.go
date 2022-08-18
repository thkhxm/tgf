package tframework

//***************************************************
//author tim.huang
//2022/8/18
//
//
//***************************************************

type ILogPlugin interface {
	Info(msg string)
	FInfo(msg string, params ...interface{})
}
