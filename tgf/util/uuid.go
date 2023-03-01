package util

import (
	"github.com/bwmarrin/snowflake"
	"math/rand"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/25
//***************************************************

var Snowflake *snowflake.Node

func GenerateSnowflakeId() string {
	return Snowflake.Generate().Base64()
}

func initSnowFlake() {
	//初始化雪花算法Id
	source := rand.NewSource(time.Now().UnixNano())
	ran := rand.New(source)
	Snowflake, _ = snowflake.NewNode(ran.Int63n(1024))
}
