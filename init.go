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
var closeChan chan bool

type IDestroyHandler interface {
	Destroy()
}

func AddDestroyHandler(handler IDestroyHandler) {
	destroyList = append(destroyList, handler)
}

func CloseChan() <-chan bool {
	return closeChan
}

func init() {
	InitConfig()
	destroyList = make([]IDestroyHandler, 0)
	closeChan = make(chan bool, 1)

	util.Go(func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, os.Kill)
		<-c
		for _, handler := range destroyList {
			handler.Destroy()
		}
		closeChan <- true
	})
}
