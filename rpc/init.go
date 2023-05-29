// Package rpc
// @Description: rpc的封装，使用了rpcx框架
// @Link: https://doc.rpcx.io/part4/alias.html rpcx框架
// @Ref:
package rpc

import (
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"github.com/thkhxm/tgf/util"
	"math/rand"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/23
//***************************************************

func init() {
	rand.Seed(time.Now().UnixMilli())
	tgf.NodeId = util.GenerateSnowflakeId()
	log.InfoTag("init", "node id %v", tgf.NodeId)
}
