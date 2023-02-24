package db

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
)

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ 277949041
// author tim.huang<thkhxm@gmail.com>
// @Description
// 2023/2/24
// ***************************************************

var service *redisService

type redisService struct {
	client *redis.Client
}

func (this *redisService) Get(key string) (res string) {
	var (
		err error
	)
	if res, err = this.client.Get(context.Background(), key).Result(); err == nil {
		return
	}
	log.Error("[redis] 获取缓存数据异常 key=%v,err=%v", key, err)
	return
}

func (this *redisService) Run() (bool, error) {
	service = new(redisService)
	service.client = redis.NewClient(&redis.Options{
		Network:  "tcp",
		Addr:     tgf.GetStrConfig[string](tgf.EnvironmentRedisAddr),
		Password: tgf.GetStrConfig[string](tgf.EnvironmentRedisPassword),
		DB:       tgf.GetStrConfig[int](tgf.EnvironmentRedisAddr),
	})
	return true, nil
}
