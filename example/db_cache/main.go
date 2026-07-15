// tgf v2 示例：内存缓存、Redis 与 MySQL write-behind。
//
// 默认只运行无外部依赖的内存/补偿队列场景。设置
// TGF_EXAMPLE_EXTERNAL=1 后才会尝试 Redis/MySQL，且会明确报告成功或失败，
// 不把“跳过”当成“已验证”。
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/thkhxm/tgf/v2/db"
)

type UserData struct {
	db.Model
	Uid      string `orm:"pk"`
	Nickname string
	Level    int
}

func (UserData) GetTableName() string { return "t_user" }

func main() {
	fmt.Println("=== tgf v2 db 场景 ===")
	runMemoryCache()
	runFailureQueue()

	if os.Getenv("TGF_EXAMPLE_EXTERNAL") != "1" {
		fmt.Println("[external] Redis/MySQL/write-behind 未执行。")
		fmt.Println("[external] 启动依赖、配置 .env.<TGFMODULE> 并设置 TGF_EXAMPLE_EXTERNAL=1 后再运行。")
		return
	}

	db.Run()
	runRedisProbe()
	runWriteBehindProbe()
}

func runMemoryCache() {
	cache := db.NewAutoCacheBuilder[string, *UserData]().WithMemCache(60).New()
	cache.Set(&UserData{Uid: "u001", Nickname: "Alice", Level: 10}, "u001")
	user, err := cache.Get("u001")
	if err != nil {
		fmt.Printf("[unit] memory cache failed: %v\n", err)
		return
	}
	fmt.Printf("[unit] memory cache: %s level=%d\n", user.Nickname, user.Level)
}

func runFailureQueue() {
	queue := db.NewMemoryFailureQueue()
	payload := db.FailurePayload([]byte(`{"table":"t_user","count":1,"values":["u001","Alice"]}`))
	if err := queue.Enqueue(payload); err != nil {
		fmt.Printf("[unit] failure queue enqueue failed: %v\n", err)
		return
	}
	if err := db.ReplayFailureQueue(queue, func(p db.FailurePayload) error {
		doc, err := db.DecodeFailurePayload(p)
		if err == nil {
			fmt.Printf("[unit] replay table=%s count=%d\n", doc.Table, doc.Count)
		}
		return err
	}); err != nil {
		fmt.Printf("[unit] failure queue replay failed: %v\n", err)
	}
}

func runRedisProbe() {
	const key = "example:greeting"
	db.Set(key, "hello tgf", time.Minute)
	value, ok := db.Get[string](key)
	if !ok || value != "hello tgf" {
		fmt.Println("[integration] Redis 读写未验证；请检查 Redis 连接与日志。")
		return
	}
	fmt.Printf("[integration] Redis 读写通过: %s\n", value)
	db.Del(key)
}

func runWriteBehindProbe() {
	queue := db.NewMemoryFailureQueue()
	manager := newWriteBehindManager(queue)
	if ok := manager.Set(&UserData{Uid: "writebehind_probe", Nickname: "Probe", Level: 1}, "writebehind_probe"); !ok {
		fmt.Println("[integration] write-behind Set 未接收；请检查 Redis/MySQL 连接。")
		return
	}
	fmt.Println("[integration] write-behind 已排队；请从 MySQL t_user 与日志确认最终落库。")
}

// newWriteBehindManager 是受 go test/go vet 编译检查的完整装配；
// 只在显式开启外部集成模式后才会被调用。
func newWriteBehindManager(queue db.FailureQueue) db.IAutoCacheService[string, *UserData] {
	return db.NewAutoCacheBuilder[string, *UserData]().
		WithMemCache(3600).
		WithAutoCache("user", 24*time.Hour).
		WithLongevityCache(5 * time.Second).
		WithLongevityGroupSize(200).
		WithLongevityRetry(3).
		WithLongevityFailureQueue(queue).
		New()
}
