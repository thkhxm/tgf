package db

import (
	"github.com/thkhxm/tgf/util"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/24
//***************************************************

var cache ICacheService

type ICacheService interface {
	Get(key string) (res string)
}

// Get [Res any]
// @Description: 通过二级缓存获取数据
// @param key
// @return res
func Get[Res any](key string) (res Res) {
	val := cache.Get(key)
	res, _ = util.StrToAny[Res](val)
	return
}
