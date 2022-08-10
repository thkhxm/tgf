package tcore

//***************************************************
//author tim.huang
//2022/8/10
//
//
//***************************************************

type TServerStatus int8

const (
	// StartBefore 启动前
	StartBefore TServerStatus = iota
	// StartAfter 启动后
	StartAfter
)

// TServerModule 模块启动配置，最多支持64个模块的配置
type TServerModule int64

// val & TServerModule == TServerModule 判断模块是否开启
const (
	Log TServerModule = 1 << iota
)
