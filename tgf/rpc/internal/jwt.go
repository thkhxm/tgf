package internal

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/25
//***************************************************

type LoginCheck struct {
}

func (l LoginCheck) CheckLogin(token string) (bool, string) {
	return true, token
}
