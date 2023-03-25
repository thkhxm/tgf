package hallentity

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/25
//***************************************************

type UserModel struct {
	Uid  string `orm:primaryKey`
	Name string
}

type User struct {
	UserModel
}
