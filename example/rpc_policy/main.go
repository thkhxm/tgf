// tgf v2 示例：rpc 包 — RPC 策略化（限流 / 熔断 / 并发控制）
//
// 演示 MethodPolicy + SendRPCMessage 在单进程模式下的策略检查。
// 展示限流拒绝、熔断触发、并发上限三种场景。
//
// 运行：cd tgf/example/rpc_policy && go run .
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"context"
	"github.com/thkhxm/tgf/v2/rpc"
)

// ====================================================================
// 1. 定义 Service + RPC
// ====================================================================

type DemoService struct {
	rpc.Module
	callCount atomic.Int32
}

func (d *DemoService) Startup() (bool, error) { return true, nil }

type Req struct{ Data string }
type Res struct{ Echo string }

func (d *DemoService) Echo(ctx context.Context, req *Req, reply *Res) error {
	d.callCount.Add(1)
	reply.Echo = "ok:" + req.Data
	return nil
}

// SlowMethod 模拟慢方法
func (d *DemoService) SlowMethod(ctx context.Context, req *Req, reply *Res) error {
	time.Sleep(100 * time.Millisecond)
	reply.Echo = "slow:" + req.Data
	return nil
}

// FailMethod 总是失败
func (d *DemoService) FailMethod(ctx context.Context, req *Req, reply *Res) error {
	return fmt.Errorf("故意失败")
}

var EchoAPI = &rpc.ServiceAPI[*Req, *Res]{
	ModuleName: "demo", Name: "Echo", MessageType: "demo.Echo",
}
var SlowAPI = &rpc.ServiceAPI[*Req, *Res]{
	ModuleName: "demo", Name: "SlowMethod", MessageType: "demo.SlowMethod",
}
var FailAPI = &rpc.ServiceAPI[*Req, *Res]{
	ModuleName: "demo", Name: "FailMethod", MessageType: "demo.FailMethod",
}

func main() {
	fmt.Println("=== tgf v2 示例：RPC 策略化 ===")
	fmt.Println()

	// 启动单进程 server
	svc := &DemoService{Module: rpc.Module{Name: "demo", Version: "v1"}}
	done := rpc.NewRPCServer().
		WithSingleProcess().
		WithService(svc).
		// ------ 策略配置 ------
		// Echo: 限流 5 QPS
		WithMethodPolicy("demo.Echo", rpc.MethodPolicy{
			Timeout:   2 * time.Second,
			RateLimit: 5,
		}).
		// SlowMethod: 最大并发 2
		WithMethodPolicy("demo.SlowMethod", rpc.MethodPolicy{
			Timeout:        3 * time.Second,
			MaxConcurrency: 2,
		}).
		// FailMethod: 熔断器（连续 3 次失败开路，恢复间隔 1 秒）
		WithMethodPolicy("demo.FailMethod", rpc.MethodPolicy{
			Timeout: 2 * time.Second,
			CircuitBreaker: rpc.CircuitBreakerConfig{
				FailureThreshold: 3,
				RecoveryInterval: 1 * time.Second,
			},
		}).
		Run()
	time.Sleep(200 * time.Millisecond)
	fmt.Println()

	ctx := context.Background()

	// ----------------------------------------------------------------
	// 场景 1：限流（RateLimit）
	// ----------------------------------------------------------------
	fmt.Println("--- 场景 1: 限流（5 QPS）---")

	passCount, rejectCount := 0, 0
	for i := 0; i < 10; i++ {
		_, err := rpc.SendRPCMessage(ctx, EchoAPI.NewRPC(&Req{Data: fmt.Sprintf("r%d", i)}))
		if err != nil {
			if errors.Is(err, rpc.ErrRPCRateLimited) {
				rejectCount++
			} else {
				fmt.Printf("  其他错误: %v\n", err)
			}
		} else {
			passCount++
		}
	}
	fmt.Printf("  连续 10 次调用: %d 通过, %d 被限流\n", passCount, rejectCount)
	fmt.Println()

	// ----------------------------------------------------------------
	// 场景 2：并发控制（MaxConcurrency）
	// ----------------------------------------------------------------
	fmt.Println("--- 场景 2: 并发控制（最大 2）---")

	var wg sync.WaitGroup
	var slowPass, slowReject atomic.Int32
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func(id int) {
			defer wg.Done()
			_, err := rpc.SendRPCMessage(ctx, SlowAPI.NewRPC(&Req{Data: fmt.Sprintf("s%d", id)}))
			if err != nil {
				if errors.Is(err, rpc.ErrRPCOverload) {
					slowReject.Add(1)
				}
			} else {
				slowPass.Add(1)
			}
		}(i)
	}
	wg.Wait()
	fmt.Printf("  5 个并发慢调用: %d 通过, %d 被拒（超并发）\n",
		slowPass.Load(), slowReject.Load())
	fmt.Println()

	// ----------------------------------------------------------------
	// 场景 3：熔断器（CircuitBreaker）
	// ----------------------------------------------------------------
	fmt.Println("--- 场景 3: 熔断器 ---")

	// 连续 3 次失败 → 触发开路
	for i := 0; i < 3; i++ {
		_, err := rpc.SendRPCMessage(ctx, FailAPI.NewRPC(&Req{Data: "fail"}))
		fmt.Printf("  第 %d 次失败调用: %v\n", i+1, err)
	}

	// 第 4 次应该被熔断器拒绝
	_, err := rpc.SendRPCMessage(ctx, FailAPI.NewRPC(&Req{Data: "fail"}))
	if errors.Is(err, rpc.ErrRPCCircuitOpen) {
		fmt.Println("  第 4 次: 被熔断器拦截 ✓")
	} else {
		fmt.Printf("  第 4 次: %v\n", err)
	}

	// 等恢复间隔后进入 HalfOpen
	fmt.Println("  等待 1.2 秒（恢复间隔）...")
	time.Sleep(1200 * time.Millisecond)

	_, err = rpc.SendRPCMessage(ctx, FailAPI.NewRPC(&Req{Data: "fail"}))
	fmt.Printf("  HalfOpen 探测: %v（失败则重回 Open）\n", err)
	fmt.Println()

	// ----------------------------------------------------------------
	// 策略总结
	// ----------------------------------------------------------------
	fmt.Println("--- 策略 API 总结 ---")
	fmt.Println(`
  // 构建时配置
  rpc.NewRPCServer().
      WithMethodPolicy("module.Method", rpc.MethodPolicy{
          Timeout:        3 * time.Second,  // RPC 超时
          MaxConcurrency: 100,              // 最大并发数（信号量）
          RateLimit:      500,              // QPS 上限（令牌桶）
          CircuitBreaker: rpc.CircuitBreakerConfig{
              FailureThreshold: 5,              // 连续失败次数
              RecoveryInterval: 30 * time.Second, // 开路持续时间
          },
      })

  // 运行时动态配置
  rpc.SetMethodPolicy("module.Method", rpc.MethodPolicy{...})

  // 错误值
  rpc.ErrRPCOverload     → MaxConcurrency 达上限
  rpc.ErrRPCRateLimited  → RateLimit 令牌耗尽
  rpc.ErrRPCCircuitOpen  → 熔断器打开

  // 策略管道在 SendRPCMessage 内部透明执行：
  //   熔断器 → 限流 → 并发信号量 → 实际调用 → release(err)
  `)

	fmt.Println("=== RPC 策略化示例结束 ===")
	fmt.Println("按 Ctrl+C 触发框架优雅停机。")
	<-done
}
