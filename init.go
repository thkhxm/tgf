// Package tgf
// @Description: 框架基础包
package tgf

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/thkhxm/tgf/util"
)

// ***************************************************
// @Link  https://github.com/thkhxm/tgf
// @Link  https://gitee.com/timgame/tgf
// @QQ群 7400585
// author tim.huang<thkhxm@gmail.com>
// @Description init
// 2023/2/22
// ***************************************************

// D3 / P0-3 优雅停机编排说明：
//
// 收到 SIGTERM/SIGINT 后的全局停机序列（顺序敏感）：
//  1. 按注册顺序触发 destroyList 中的 IDestroyHandler——rpc.Server 在 NewRPCServer
//     时最先注册，其 Destroy 内部完成「Consul 反注册（摘流量）→ 网关停止 accept →
//     drain in-flight（带可配置超时）→ 第一轮终末 flush → 业务 service Destroy」；
//  2. 全部 handler 跑完后再执行 finalFlushHooks（tgf/rpc 包桥接的 db.FlushAll），
//     兜住业务 Destroy 期间新产生的脏数据；
//  3. 全程由 shutdownTimeout 看门狗兜底——序列卡死时以非零退出码强制退出，
//     避免容器编排层等到 terminationGracePeriod 被 SIGKILL 时进程仍假装健康。
//
// 退出码约定：优雅完成 → 通知 CloseChan，主线 <-Run() 返回后 main 自然结束（退出码 0）；
// 看门狗超时 / 第二次信号 → os.Exit(1)。
//
// 注意：本包不能 import tgf/log（log 依赖 tgf，会形成循环依赖），
// 停机路径上的日志直接写 stderr。

const (
	// defaultShutdownTimeout 整个优雅停机序列的默认总超时（看门狗）。
	// 业务可通过 SetShutdownTimeout / rpc.Server.WithShutdownTimeout 覆盖；
	// 该值应大于 drain in-flight 阶段的超时（见 rpc.Server.WithShutdownDrainTimeout）。
	defaultShutdownTimeout = 60 * time.Second
	// exitCodeShutdownTimeout 优雅停机看门狗超时后的强制退出码。
	exitCodeShutdownTimeout = 1
	// exitCodeForcedExit 停机期间收到第二次信号后的强制退出码。
	exitCodeForcedExit = 1
)

// shutdownSignals 触发优雅停机的信号集合。
//
// D3 / P0-3 修复：原实现注册的是 os.Interrupt + os.Kill——os.Kill（SIGKILL）本就
// 不可被捕获，注册了也是死代码；而容器/K8s 滚动发布发送的 SIGTERM 完全没注册，
// 停机清理（摘流量 / drain / 终末落库）在每次常规发布中都不会触发，
// 固定丢失最后一个落库窗口（≤5s）的脏数据。现在改为 SIGINT + SIGTERM。
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

var (
	// destroyMu 保护 destroyList / finalFlushHooks / shutdownTimeout 的并发注册与读取。
	destroyMu   sync.Mutex
	destroyList []IDestroyHandler
	// finalFlushHooks 在所有 IDestroyHandler 执行完毕之后运行的终末钩子列表。
	// tgf 根包因循环依赖不能直接调用 db.FlushAll，由 tgf/rpc 包在 init 时桥接注册。
	finalFlushHooks []func()
	// shutdownTimeout 优雅停机看门狗超时，SetShutdownTimeout 可覆盖。
	shutdownTimeout = defaultShutdownTimeout

	closeChan chan bool
)

type IDestroyHandler interface {
	Destroy()
}

func AddDestroyHandler(handler IDestroyHandler) {
	if handler == nil {
		return
	}
	destroyMu.Lock()
	defer destroyMu.Unlock()
	destroyList = append(destroyList, handler)
}

// RegisterFinalFlushHook 注册一个在所有 IDestroyHandler 执行完毕后运行的终末钩子。
// 典型使用方：tgf/rpc 在包 init 时把 db.FlushAll 桥接进来（本包不能直接 import
// tgf/db，会形成循环依赖）。钩子内部 panic 会被捕获，不会中断后续钩子或停机流程。
func RegisterFinalFlushHook(f func()) {
	if f == nil {
		return
	}
	destroyMu.Lock()
	defer destroyMu.Unlock()
	finalFlushHooks = append(finalFlushHooks, f)
}

// FinalFlushHookCount 返回当前已注册的终末 flush 钩子数量（诊断/测试用）。
func FinalFlushHookCount() int {
	destroyMu.Lock()
	defer destroyMu.Unlock()
	return len(finalFlushHooks)
}

// SetShutdownTimeout 配置优雅停机看门狗的总超时；d <= 0 时忽略。
// 必须在收到停机信号之前调用（通常在进程启动阶段）；
// rpc.Server.WithShutdownTimeout 是它的 builder 风格入口。
func SetShutdownTimeout(d time.Duration) {
	if d <= 0 {
		return
	}
	destroyMu.Lock()
	defer destroyMu.Unlock()
	shutdownTimeout = d
}

// getShutdownTimeout 读取当前看门狗超时的快照。
func getShutdownTimeout() time.Duration {
	destroyMu.Lock()
	defer destroyMu.Unlock()
	return shutdownTimeout
}

func CloseChan() <-chan bool {
	return closeChan
}

// runShutdownSequence 按序执行全部 IDestroyHandler 与终末 flush 钩子。
// 单个 handler / 钩子 panic 只影响自身——绝不能让一个业务 Destroy 的 panic
// 跳过后续清理（尤其是终末落库），否则等价于旧 bug 的"丢最后一个窗口"。
func runShutdownSequence() {
	destroyMu.Lock()
	handlers := make([]IDestroyHandler, len(destroyList))
	copy(handlers, destroyList)
	hooks := make([]func(), len(finalFlushHooks))
	copy(hooks, finalFlushHooks)
	destroyMu.Unlock()

	// 1. 按注册顺序触发 IDestroyHandler。rpc.Server 通常最先注册——它的 Destroy
	//    内部完成摘流量/停 accept/drain/第一轮 flush，业务 handler 随后执行。
	for _, handler := range handlers {
		safeDestroy(handler)
	}
	// 2. 全部 handler 完成后执行终末 flush 钩子（db.FlushAll 桥接），
	//    兜住 Destroy 期间业务代码新产生的脏数据。
	for _, hook := range hooks {
		safeRunHook(hook)
	}
}

// safeDestroy 带 recover 地执行单个 IDestroyHandler。
func safeDestroy(h IDestroyHandler) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[tgf][shutdown] destroy handler panic(已忽略,继续后续清理): %v\n", r)
		}
	}()
	h.Destroy()
}

// safeRunHook 带 recover 地执行单个终末 flush 钩子。
func safeRunHook(f func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[tgf][shutdown] 终末 flush 钩子 panic(已忽略,继续后续钩子): %v\n", r)
		}
	}()
	f()
}

// gracefulShutdown 是信号触发后的停机编排入口。
// 参数全部显式传入以便单测注入：
//
//	sigCh   后续信号通道——停机期间收到第二次 SIGTERM/SIGINT 立即强制退出
//	timeout 看门狗超时——停机序列卡死时以非零码退出，避免进程僵死
//	exitFn  退出函数（生产传 os.Exit，测试注入桩记录退出码）
//	notify  优雅完成后的通知通道（即 CloseChan；主线 <-Run() 返回，进程以 0 码退出）
func gracefulShutdown(sigCh <-chan os.Signal, timeout time.Duration, exitFn func(int), notify chan<- bool) {
	done := make(chan struct{})
	util.Go(func() {
		runShutdownSequence()
		close(done)
	})

	select {
	case <-done:
		// 优雅完成：通知主线退出（Run() 返回，main 自然结束 → 退出码 0）。
		// 非阻塞发送：业务若没有阻塞在 <-Run() 上，也不能让停机流程卡死在这里。
		select {
		case notify <- true:
		default:
		}
	case <-time.After(timeout):
		fmt.Fprintf(os.Stderr, "[tgf][shutdown] 优雅停机超时(%v),强制退出 exitCode=%v\n", timeout, exitCodeShutdownTimeout)
		exitFn(exitCodeShutdownTimeout)
	case s := <-sigCh:
		fmt.Fprintf(os.Stderr, "[tgf][shutdown] 停机期间收到第二次信号(%v),放弃等待立即退出 exitCode=%v\n", s, exitCodeForcedExit)
		exitFn(exitCodeForcedExit)
	}
}

func init() {
	InitConfig()
	destroyList = make([]IDestroyHandler, 0)
	closeChan = make(chan bool, 1)

	util.Go(func() {
		// D3 / P0-3：捕获 SIGINT + SIGTERM（容器/K8s 滚动发布走 SIGTERM）。
		// channel 容量 2：第一个信号触发优雅停机，第二个信号触发强制退出。
		c := make(chan os.Signal, 2)
		signal.Notify(c, shutdownSignals...)
		sig := <-c
		fmt.Fprintf(os.Stderr, "[tgf][shutdown] 收到停机信号(%v),开始优雅停机(看门狗超时=%v)\n", sig, getShutdownTimeout())
		gracefulShutdown(c, getShutdownTimeout(), os.Exit, closeChan)
	})
}
