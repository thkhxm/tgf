package db

import (
	"context"
	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/log"
	"strings"
	"time"
)

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ群 7400585
// author tim.huang<thkhxm@gmail.com>
// @Description
// 2023/2/24
// ***************************************************

var service *redisService

type redisService struct {
	client redis.UniversalClient
}

func (r *redisService) Get(key string) (res string) {
	var (
		err error
	)
	if res, err = r.client.Get(context.Background(), key).Result(); err == nil || err == redis.Nil {
		return
	}
	log.Error("[redis] 获取缓存数据异常 key=%v,err=%v", key, err)
	return
}

func (r *redisService) Set(key string, val interface{}, timeout time.Duration) {
	r.client.Set(context.Background(), key, val, timeout)
}

func (r *redisService) GetMap(key string) map[string]string {
	res, _ := r.client.HGetAll(context.Background(), key).Result()
	return res
}

func (r *redisService) PutMap(key, filed, val string, timeout time.Duration) {
	var ()
	r.client.HSet(context.Background(), key, filed, val)
	if timeout > 0 {
		r.client.Expire(context.Background(), key, timeout)
	}
}

func (r *redisService) Del(key string) {
	var ()
	r.client.Expire(context.Background(), key, time.Second)
}

func (r *redisService) DelNow(key string) {
	var ()
	r.client.Del(context.Background(), key)
}

func (r *redisService) GetList(key string, start, end int64) (res []string, err error) {
	var ()
	res, err = r.client.LRange(context.Background(), key, start, end).Result()
	return
}

func (r *redisService) SetList(key string, l []interface{}, timeout time.Duration) {
	var ()
	r.client.RPush(context.Background(), key, l...)
	if timeout > 0 {
		r.client.Expire(context.Background(), key, timeout)
	}
}

func (r *redisService) AddListItem(key string, val string) {
	var ()
	r.client.LPush(context.Background(), key, val)
}

func (r *redisService) TryLock(key string) (*redislock.Lock, error) {
	var ()
	lock := redislock.New(service.client)
	return lock.Obtain(context.Background(), key, time.Second*5, nil)
}

func (r *redisService) TryUnLock(l *redislock.Lock, ctx context.Context) {
	var ()
	l.Release(ctx)
}
func newRedisService() *redisService {
	var (
		addr     = tgf.GetStrConfig[string](tgf.EnvironmentRedisAddr)
		password = tgf.GetStrConfig[string](tgf.EnvironmentRedisPassword)
		db       = tgf.GetStrConfig[int](tgf.EnvironmentRedisDB)
	)

	service = new(redisService)
	redisOptions := &redis.UniversalOptions{}

	redisOptions.Addrs = strings.Split(addr, ",")
	redisOptions.DB = db
	if password != "" {
		redisOptions.Password = password
	}
	service.client = redis.NewUniversalClient(redisOptions)
	if stat := service.client.Ping(context.Background()); stat.Err() != nil {
		log.WarnTag("init", "启动redis服务异常 addr=%v db=%v err=%v", addr, db, stat.Err())
		return nil
	}

	log.InfoTag("init", "启动redis服务 addr=%v db=%v", addr, db)
	return service
}
