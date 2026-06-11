// tgf v2 示例：db 包 — 自动缓存管理器
//
// 演示 AutoCacheBuilder 的三种缓存模式 + 基础 Redis 操作 + 分布式锁 + 补偿队列。
// 注意：本示例的 Redis/MySQL 操作在没有真实服务时会跳过，不会 panic。
//
// 运行：cd tgf/example/db_cache && go run .
package main

import (
	"fmt"
	"time"

	"github.com/thkhxm/tgf/v2/db"
)

// ====================================================================
// 1. 定义一个业务数据模型（配合 MySQL 持久化时需要实现 IModel）
// ====================================================================

// UserData 演示一个最小的缓存数据结构。
// - 第一个字段带 `orm:"pk"` 标签表示主键
// - 实现 GetTableName() 对应 MySQL 表名
type UserData struct {
	db.Model
	Uid      string `orm:"pk"`
	Nickname string
	Level    int
}

func (u UserData) GetTableName() string {
	return "t_user"
}

func main() {
	fmt.Println("=== tgf v2 示例：db 包 ===")
	fmt.Println()

	// 初始化 db 层（会尝试连 Redis/MySQL，失败只打警告不 panic）
	db.Run()

	// ----------------------------------------------------------------
	// 示例 1：纯内存缓存（不依赖 Redis/MySQL）
	// ----------------------------------------------------------------
	fmt.Println("--- 示例 1: 纯内存缓存 ---")

	memCache := db.NewAutoCacheBuilder[string, *UserData]().
		WithMemCache(60). // 60 秒过期
		New()

	// Set：写入缓存
	memCache.Set(&UserData{Uid: "u001", Nickname: "Alice", Level: 10}, "u001")
	memCache.Set(&UserData{Uid: "u002", Nickname: "Bob", Level: 20}, "u002")

	// Get：读取缓存
	if user, err := memCache.Get("u001"); err == nil {
		fmt.Printf("  Get u001: Nickname=%s Level=%d\n", user.Nickname, user.Level)
	}

	// Range：遍历所有缓存条目
	fmt.Println("  Range 遍历:")
	memCache.Range(func(key string, val *UserData) bool {
		fmt.Printf("    key=%s → %s (Lv%d)\n", key, val.Nickname, val.Level)
		return true // 返回 false 中断遍历
	})

	// Remove：删除
	memCache.Remove("u002")
	if _, err := memCache.Get("u002"); err != nil {
		fmt.Println("  u002 已删除")
	}

	// Reset：清空全部
	memCache.Reset()
	fmt.Println()

	// ----------------------------------------------------------------
	// 示例 2：内存 + Redis 二级缓存
	// ----------------------------------------------------------------
	fmt.Println("--- 示例 2: 内存 + Redis 缓存 ---")
	fmt.Println("  (需要 Redis，无 Redis 时 Set/Get 会降级到纯内存)")

	redisCache := db.NewDefaultAutoCacheManager[string, int64]("score")
	redisCache.Set(int64(9999), "u001")
	if score, err := redisCache.Get("u001"); err == nil {
		fmt.Printf("  Get score u001: %d\n", score)
	}
	fmt.Println()

	// ----------------------------------------------------------------
	// 示例 3：基础 Redis 操作（Key-Value / Map / List）
	// ----------------------------------------------------------------
	fmt.Println("--- 示例 3: 基础 Redis 操作 ---")
	fmt.Println("  (需要 Redis，无 Redis 时静默跳过)")

	// Key-Value
	db.Set("example:greeting", "hello tgf", time.Minute*5)
	if val, ok := db.Get[string]("example:greeting"); ok {
		fmt.Printf("  KV Get: %s\n", val)
	}

	// Hash Map
	db.PutMap("example:profile", "name", "Charlie", time.Minute*5)
	db.PutMap("example:profile", "age", "25", time.Minute*5)
	if m, ok := db.GetMap[string, string]("example:profile"); ok {
		fmt.Printf("  Map: %v\n", m)
	}

	// List
	db.AddListItem("example:items", time.Minute*5, "sword", "shield", "potion")
	items := db.GetList[string]("example:items")
	fmt.Printf("  List: %v\n", items)

	// 清理
	db.Del("example:greeting")
	db.Del("example:profile")
	db.Del("example:items")
	fmt.Println()

	// ----------------------------------------------------------------
	// 示例 4：FormatKey 拼接 Redis key
	// ----------------------------------------------------------------
	fmt.Println("--- 示例 4: Key 拼接 ---")
	key := db.FormatKey("user", "u001", "inventory")
	fmt.Printf("  FormatKey: %s\n", key) // user:u001:inventory
	fmt.Println()

	// ----------------------------------------------------------------
	// 示例 5：分布式锁
	// ----------------------------------------------------------------
	fmt.Println("--- 示例 5: 分布式锁 ---")
	fmt.Println("  (需要 Redis)")
	lock, err := db.NewLock("example:lock:order:1001")
	if err != nil {
		fmt.Printf("  获取锁失败（可能无 Redis）: %v\n", err)
	} else {
		fmt.Println("  成功获取锁")
		// ... 临界区操作 ...
		db.UnLock(lock)
		fmt.Println("  锁已释放")
	}
	fmt.Println()

	// ----------------------------------------------------------------
	// 示例 6：补偿队列（C4/A1b）
	// ----------------------------------------------------------------
	fmt.Println("--- 示例 6: 补偿队列 ---")

	// 创建一个内存队列（生产用 FileFailureQueue）
	queue := db.NewMemoryFailureQueue()

	// 模拟落库失败后入队
	payload := []byte(`{"table":"t_user","count":1,"values":["u001","Alice"]}`)
	_ = queue.Enqueue(db.FailurePayload(payload))
	_ = queue.Enqueue(db.FailurePayload([]byte(`{"table":"t_user","count":1,"values":["u002","Bob"]}`)))
	fmt.Printf("  队列长度: %d\n", queue.Len())

	// 启动时重放
	err = db.ReplayFailureQueue(queue, func(p db.FailurePayload) error {
		doc, _ := db.DecodeFailurePayload(p)
		fmt.Printf("  重放: table=%s count=%d\n", doc.Table, doc.Count)
		return nil // 返回 error 则剩余条目重新入队
	})
	if err != nil {
		fmt.Printf("  重放失败: %v\n", err)
	}
	fmt.Printf("  重放后队列长度: %d\n", queue.Len())
	fmt.Println()

	// ----------------------------------------------------------------
	// 示例 7：AutoCacheBuilder 完整配置链
	// ----------------------------------------------------------------
	fmt.Println("--- 示例 7: 完整 Builder 配置链 ---")
	fmt.Println("  (仅展示 API，不执行)")
	fmt.Println(`
  builder := db.NewAutoCacheBuilder[string, *UserData]().
      WithMemCache(3600).                           // 内存缓存 1 小时
      WithAutoCache("user", 24*time.Hour).          // Redis 缓存 1 天
      WithLongevityCache(5*time.Second).             // 每 5 秒落库
      WithLongevityGroupSize(200).                   // 每批最多 200 条
      WithLongevityRetry(3).                         // 失败重试 3 次
      WithLongevityFailureQueue(queue).              // 耗尽重试后入补偿队列
      New()
  `)

	fmt.Println("=== db 示例结束 ===")
}
