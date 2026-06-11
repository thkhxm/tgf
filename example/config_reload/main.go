// tgf v2 示例：config 包 — struct tag 驱动的配置系统
//
// 演示 Load / Current / Reload / OnReload 的完整流程。
// 通过在代码里临时设置环境变量来模拟不同环境。
//
// 运行：cd tgf/example/config_reload && go run .
package main

import (
	"fmt"
	"os"

	"github.com/thkhxm/tgf/v2/config"
)

func main() {
	fmt.Println("=== tgf v2 示例：config 包 ===")
	fmt.Println()

	// ----------------------------------------------------------------
	// 1. 首次加载（全部走默认值）
	// ----------------------------------------------------------------
	fmt.Println("--- 1. 首次加载（默认值）---")

	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("加载失败: %v\n", err)
		return
	}

	fmt.Printf("  Logger.Path:    %s\n", cfg.Logger.Path)     // ./log/tgf.log
	fmt.Printf("  Logger.Level:   %s\n", cfg.Logger.Level)    // debug
	fmt.Printf("  Redis.Addr:     %s\n", cfg.Redis.Addr)      // 127.0.0.1:6379
	fmt.Printf("  Redis.DB:       %d\n", cfg.Redis.DB)        // 1
	fmt.Printf("  Redis.Cluster:  %v\n", cfg.Redis.Cluster)   // false
	fmt.Printf("  MySQL.User:     %s\n", cfg.MySQL.User)      // root
	fmt.Printf("  MySQL.Port:     %s\n", cfg.MySQL.Port)      // 3306
	fmt.Printf("  Consul.Address: %s\n", cfg.Consul.Address)  // 127.0.0.1:8500
	fmt.Printf("  Service.Port:   %s\n", cfg.Service.Port)    // 8082
	fmt.Printf("  Gate.Push:      %v\n", cfg.Gate.Push)        // true
	fmt.Printf("  Runtime.Module: %s\n", cfg.Runtime.Module)  // dev
	fmt.Println()

	// ----------------------------------------------------------------
	// 2. 通过 Current() 读快照
	// ----------------------------------------------------------------
	fmt.Println("--- 2. Current() 快照 ---")

	c := config.Current()
	fmt.Printf("  Current().Logger.Level = %s\n", c.Logger.Level)
	fmt.Printf("  Current() == cfg: %v\n", c == cfg) // true，同一个指针
	fmt.Println()

	// ----------------------------------------------------------------
	// 3. 设置环境变量 → Reload → OnReload 回调
	// ----------------------------------------------------------------
	fmt.Println("--- 3. Reload 热更 ---")

	// 注册一个回调
	config.OnReload(func(newCfg *config.Config) {
		fmt.Printf("  [OnReload 回调] 新的 Logger.Level = %s\n", newCfg.Logger.Level)
		fmt.Printf("  [OnReload 回调] 新的 Redis.Addr   = %s\n", newCfg.Redis.Addr)
	})

	// 模拟环境变量变化
	os.Setenv("LogLevel", "warn")
	os.Setenv("RedisAddr", "10.0.0.1:6380")
	os.Setenv("RedisDB", "5")
	os.Setenv("RedisCluster", "1")

	newCfg, err := config.Reload()
	if err != nil {
		fmt.Printf("Reload 失败: %v\n", err)
		return
	}
	fmt.Printf("  Reload 后 Logger.Level: %s\n", newCfg.Logger.Level)   // warn
	fmt.Printf("  Reload 后 Redis.Addr:   %s\n", newCfg.Redis.Addr)     // 10.0.0.1:6380
	fmt.Printf("  Reload 后 Redis.DB:     %d\n", newCfg.Redis.DB)       // 5
	fmt.Printf("  Reload 后 Redis.Cluster: %v\n", newCfg.Redis.Cluster) // true

	// Current() 也同步更新了
	fmt.Printf("  Current().Logger.Level = %s\n", config.Current().Logger.Level) // warn
	fmt.Println()

	// ----------------------------------------------------------------
	// 4. 类型校验：非法值报错
	// ----------------------------------------------------------------
	fmt.Println("--- 4. 类型校验 ---")

	os.Setenv("RedisDB", "not-a-number")
	_, err = config.Reload()
	if err != nil {
		fmt.Printf("  预期的类型错误: %v\n", err)
	}

	// 恢复有效值
	os.Setenv("RedisDB", "1")
	fmt.Println()

	// ----------------------------------------------------------------
	// 5. 配置结构体标签说明
	// ----------------------------------------------------------------
	fmt.Println("--- 5. struct tag 用法 ---")
	fmt.Println(`
  type RedisConfig struct {
      Addr     string ` + "`" + `env:"RedisAddr" default:"127.0.0.1:6379"` + "`" + `
      Password string ` + "`" + `env:"RedisPassword" default:""` + "`" + `
      DB       int    ` + "`" + `env:"RedisDB" default:"1"` + "`" + `
      Cluster  bool   ` + "`" + `env:"RedisCluster" default:"false"` + "`" + `
  }

  支持的 tag：
    env:"XXX"          → 对应的环境变量名
    default:"value"    → 环境变量未设置时的默认值
    required:"true"    → 必须设置（环境变量+默认值都为空则报错）

  支持的类型：
    string / int / int32 / int64 / uint32 / float64 / bool
    time.Duration（如 "250ms" / "5s" / "1m"）
    []string（逗号分隔，如 "a,b,c"）

  新增配置字段只需要改 struct 定义一处（v1 要改 3 处）。
  `)

	// 清理
	os.Unsetenv("LogLevel")
	os.Unsetenv("RedisAddr")
	os.Unsetenv("RedisDB")
	os.Unsetenv("RedisCluster")

	fmt.Println("=== config 示例结束 ===")
}
