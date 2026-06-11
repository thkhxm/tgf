// tgf v2 示例：util 包 — 常用工具函数
//
// 演示 Go/GoE 协程池、Snowflake ID、随机数、IP 获取等。
//
// 运行：cd tgf/example/util_tools && go run .
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thkhxm/tgf/v2/util"
)

func main() {
	fmt.Println("=== tgf v2 示例：util 包 ===")
	fmt.Println()

	// ----------------------------------------------------------------
	// 1. Go — 协程池投递
	// ----------------------------------------------------------------
	fmt.Println("--- 1. Go() 协程池 ---")

	var done sync.WaitGroup
	var count atomic.Int32
	done.Add(5)
	for i := 0; i < 5; i++ {
		i := i
		util.Go(func() {
			defer done.Done()
			count.Add(1)
			fmt.Printf("  协程池执行任务 %d\n", i)
		})
	}
	done.Wait()
	fmt.Printf("  完成 %d 个任务\n", count.Load())
	fmt.Println()

	// ----------------------------------------------------------------
	// 2. GoE — 带错误返回的协程池
	// ----------------------------------------------------------------
	fmt.Println("--- 2. GoE() 精确感知 ---")

	err := util.GoE(func() {
		fmt.Println("  GoE 任务执行成功")
	})
	if err != nil {
		fmt.Printf("  GoE 投递失败: %v\n", err)
	}
	time.Sleep(50 * time.Millisecond) // 等异步完成
	fmt.Println()

	// ----------------------------------------------------------------
	// 3. Snowflake ID 生成
	// ----------------------------------------------------------------
	fmt.Println("--- 3. Snowflake ID ---")

	for i := 0; i < 5; i++ {
		id := util.GenerateSnowflakeId()
		fmt.Printf("  ID[%d]: %s\n", i, id)
	}
	fmt.Println("  （Base64 编码的 Snowflake ID，全局唯一）")
	fmt.Println()

	// ----------------------------------------------------------------
	// 4. 随机数
	// ----------------------------------------------------------------
	fmt.Println("--- 4. RandNumber ---")

	// 整数
	for i := 0; i < 5; i++ {
		n := util.RandNumber[int](1, 100)
		fmt.Printf("  int[1,100]: %d\n", n)
	}

	// int64
	n64 := util.RandNumber[int64](1000000, 9999999)
	fmt.Printf("  int64: %d\n", n64)

	// int32
	n32 := util.RandNumber[int32](1, 1000)
	fmt.Printf("  int32[1,1000]: %d\n", n32)
	fmt.Println()

	// ----------------------------------------------------------------
	// 5. 本机 IP
	// ----------------------------------------------------------------
	fmt.Println("--- 5. GetLocalHost ---")

	ip := util.GetLocalHost()
	fmt.Printf("  本机 IP: %s\n", ip)
	fmt.Println()

	// ----------------------------------------------------------------
	// 6. 类型转换
	// ----------------------------------------------------------------
	fmt.Println("--- 6. 类型转换 ---")

	// string → int
	n, _ := util.StrToAny[int]("42")
	fmt.Printf("  StrToAny[int](\"42\") = %d\n", n)

	// int → string
	s, _ := util.AnyToStr(12345)
	fmt.Printf("  AnyToStr(12345) = %s\n", s)

	// JSON 字符串 → struct
	type Point struct {
		X int
		Y int
	}
	p, _ := util.StrToAny[Point](`{"X":10,"Y":20}`)
	fmt.Printf("  StrToAny[Point](...) = %+v\n", p)
	fmt.Println()

	// ----------------------------------------------------------------
	// 7. 协程池 Go 的 fallback 行为
	// ----------------------------------------------------------------
	fmt.Println("--- 7. Go() 池满 fallback ---")
	fmt.Println(`
  util.Go(f) 的行为：
  1. 先尝试投递到 ants 协程池
  2. 池满时 fallback 到裸 goroutine + stderr 告警
  3. 永远不会静默丢弃任务（v1 bug 已在 A5 修复）

  util.GoE(f) 的行为：
  1. 尝试投递到 ants 协程池
  2. 投递失败返回 error（调用方自己决定要不要 fallback）
  3. 不做自动 fallback——精确感知版本
  `)

	fmt.Println("=== util 示例结束 ===")
}
