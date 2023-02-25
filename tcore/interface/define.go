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

// TServerPlugin 模块启动配置，最多支持64个模块的配置
type TServerPlugin int64

const (
	Log    TServerPlugin = 0 //log模块，默认启动
	Consul               = 1 << iota
	P2P
	Redis
)

type TRPCType int32

const (
	Default TRPCType = 0 //默认模式,选任意一节点发送
)

// CheckServerPlugs val & TServerPlugin == TServerPlugin 判断模块是否开启
func CheckServerPlugs(base int64, val TServerPlugin) (open bool) {
	open = base&int64(val) == int64(val)
	return
}

const (
	ContextKey_UserId string = "UserId"
)

//
