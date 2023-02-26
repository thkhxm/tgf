package rpc

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

// IService
//
//	@Description: 逻辑服务接口
type IService interface {
	GetName() string
	GetVersion() string
}

type Module struct {
	name    string
	version string
}

func (this *Module) GetName() string {
	var ()
	return this.name
}

func (this *Module) GetVersion() string {
	var ()
	return this.version
}
