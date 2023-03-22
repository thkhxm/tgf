// Package tgf
// @Description: 框架基础包
package tgf

import (
	"github.com/thkhxm/tgf/util"
	"os"
	"os/signal"
)

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ群 7400585
// author tim.huang<thkhxm@gmail.com>
// @Description init
// 2023/2/22
// ***************************************************

var destroyList []IDestroyHandler

type IDestroyHandler interface {
	Destroy()
}

func AddDestroyHandler(handler IDestroyHandler) {
	destroyList = append(destroyList, handler)
}

func init() {
	InitConfig()
	destroyList = make([]IDestroyHandler, 0)
	util.Go(func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		for _, handler := range destroyList {
			handler.Destroy()
		}
	})
}
