package tframework

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

// TServerPlugs 模块启动配置，最多支持64个模块的配置
type TServerPlugs int64

const (
	Log TServerPlugs = 1 << iota
	Consul
	P2P
)

// CheckServerPlugs val & TServerPlugs == TServerPlugs 判断模块是否开启
func CheckServerPlugs(base int64, val TServerPlugs) (open bool) {
	open = base&int64(val) == int64(val)
	return
}
