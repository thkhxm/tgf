package util

import "github.com/bwmarrin/snowflake"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ 277949041
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/25
//***************************************************

var Snowflake *snowflake.Node

func GenerateSnowflakeId() string {
	return Snowflake.Generate().Base64()
}
