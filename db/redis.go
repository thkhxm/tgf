package db

import (
	"context"
	"errors"
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
	client  redis.UniversalClient
	cluster redis.ClusterClient
}

// warnRedisErr D5: redis 写路径不再静默吞错。
// 对签名不带 error 的方法（Set/PutMap/Del 等 iCacheService 接口约束），
// 至少把失败记录成 WARN 日志保证可观测；redis.Nil 属于正常的 key 不存在，不告警。
func warnRedisErr(op, key string, err error) {
	if err != nil && !errors.Is(err, redis.Nil) {
		log.WarnTag("redis", "%v 操作失败 key=%v err=%v", op, key, err)
	}
}

func (r *redisService) GetClient() redis.UniversalClient {
	return r.client
}

func (r *redisService) Get(key string) (res string) {
	var (
		err error
	)
	if res, err = r.client.Get(context.Background(), key).Result(); err == nil || errors.Is(err, redis.Nil) {
		return
	}
	log.Error("[redis] 获取缓存数据异常 key=%v,err=%v", key, err)
	return
}

func (r *redisService) Set(key string, val interface{}, timeout time.Duration) {
	warnRedisErr("Set", key, r.client.Set(context.Background(), key, val, timeout).Err())
}

func (r *redisService) GetMap(key string) map[string]string {
	res, err := r.client.HGetAll(context.Background(), key).Result()
	warnRedisErr("GetMap", key, err)
	return res
}

func (r *redisService) PutMap(key, filed, val string, timeout time.Duration) {
	warnRedisErr("PutMap", key, r.client.HSet(context.Background(), key, filed, val).Err())
	if timeout > 0 {
		warnRedisErr("PutMap/Expire", key, r.client.Expire(context.Background(), key, timeout).Err())
	}
}

func (r *redisService) Del(key string) {
	warnRedisErr("Del", key, r.client.Expire(context.Background(), key, time.Second).Err())
}

func (r *redisService) DelNow(key string) {
	warnRedisErr("DelNow", key, r.client.Del(context.Background(), key).Err())
}

func (r *redisService) GetList(key string, start, end int64) (res []string, err error) {
	var ()
	res, err = r.client.LRange(context.Background(), key, start, end).Result()
	return
}

func (r *redisService) SetList(key string, l []interface{}, timeout time.Duration) {
	warnRedisErr("SetList", key, r.client.RPush(context.Background(), key, l...).Err())
	if timeout > 0 {
		warnRedisErr("SetList/Expire", key, r.client.Expire(context.Background(), key, timeout).Err())
	}
}

func (r *redisService) AddListItem(key string, val string, timeout time.Duration) {
	warnRedisErr("AddListItem", key, r.client.LPush(context.Background(), key, val).Err())
	if timeout > 0 {
		warnRedisErr("AddListItem/Expire", key, r.client.Expire(context.Background(), key, timeout).Err())
	}
}

func (r *redisService) TryLock(key string) (*redislock.Lock, error) {
	var ()
	lock := redislock.New(r.client)
	return lock.Obtain(context.Background(), key, time.Second*5, nil)
}

func (r *redisService) TryUnLock(l *redislock.Lock, ctx context.Context) {
	var ()
	l.Release(ctx)
}

// Incr D5: 不再恒返回 nil error——redis 故障时如实把错误抛给业务，
// 避免计数器/限流器在故障期间被静默归零当真值用。
func (r *redisService) Incr(key string, timeout time.Duration) (res int64, err error) {
	fc := r.client.Incr(context.Background(), key)
	if err = fc.Err(); err != nil {
		warnRedisErr("Incr", key, err)
		return 0, err
	}
	if timeout > 0 {
		warnRedisErr("Incr/Expire", key, r.client.Expire(context.Background(), key, timeout).Err())
	}
	return fc.Val(), nil
}

func (r *redisService) IncrBy(key string, val float64, timeout time.Duration) (res float64, err error) {
	fc := r.client.IncrByFloat(context.Background(), key, val)
	if err = fc.Err(); err != nil {
		warnRedisErr("IncrBy", key, err)
		return 0, err
	}
	if timeout > 0 {
		warnRedisErr("IncrBy/Expire", key, r.client.Expire(context.Background(), key, timeout).Err())
	}
	return fc.Val(), nil
}

func (r *redisService) LLen(key string) (res int64, err error) {
	i := r.client.LLen(context.Background(), key)
	if err = i.Err(); err != nil {
		warnRedisErr("LLen", key, err)
		return 0, err
	}
	return i.Val(), nil
}

func (r *redisService) GetSet(key string) (res []string, err error) {
	data := r.client.SMembers(context.Background(), key)
	return data.Result()
}
func (r *redisService) AddSetItem(key string, val interface{}, timeout time.Duration) {
	warnRedisErr("AddSetItem", key, r.client.SAdd(context.Background(), key, val).Err())
	if timeout > 0 {
		warnRedisErr("AddSetItem/Expire", key, r.client.Expire(context.Background(), key, timeout).Err())
	}
}

// newRedisService D5: 启动失败改为显式返回 error，绝不返回 nil 的 *redisService——
// 旧实现 `return nil` 赋给 iCacheService 接口后变成 typed-nil，绕过所有
// `cache == nil` 防御，首次缓存操作直接 nil receiver panic。
func newRedisService() (*redisService, error) {
	var (
		addr     = tgf.GetStrConfig[string](tgf.EnvironmentRedisAddr)
		password = tgf.GetStrConfig[string](tgf.EnvironmentRedisPassword)
		db       = tgf.GetStrConfig[int](tgf.EnvironmentRedisDB)
		cluster  = tgf.GetStrConfig[int](tgf.EnvironmentRedisCluster)
	)

	svc := new(redisService)

	if cluster == 1 {
		redisOptions := &redis.ClusterOptions{}
		redisOptions.Addrs = strings.Split(addr, ",")
		if password != "" {
			redisOptions.Password = password
		}
		svc.client = redis.NewClusterClient(redisOptions)
	} else {
		redisOptions := &redis.UniversalOptions{}
		redisOptions.Addrs = strings.Split(addr, ",")
		redisOptions.DB = db
		if password != "" {
			redisOptions.Password = password
		}
		svc.client = redis.NewUniversalClient(redisOptions)
	}

	if stat := svc.client.Ping(context.Background()); stat.Err() != nil {
		log.WarnTag("init", "启动redis服务异常 addr=%v db=%v err=%v", addr, db, stat.Err())
		_ = svc.client.Close()
		return nil, stat.Err()
	}

	service = svc
	log.InfoTag("init", "启动redis服务 addr=%v db=%v", addr, db)
	return svc, nil
}
