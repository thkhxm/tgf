package rpc

// A3-phase2: 跨节点登录互斥 & 定向踢 owner。
//
// 本文件提供 loginCoordinator 接口封装 Login 流程依赖的几个副作用：
//   - Redis 登录锁的获取 / 释放
//   - 读 / 写 user:node:meta 里的 gate owner 字段
//   - 向远端 gate 节点定向发 Offline RPC
//
// gate.Login 只依赖这个接口，默认走 defaultLoginCoordinator（生产路径），
// 测试通过 package-level var 的替换注入 fakeLoginCoordinator。

import (
	"fmt"
	"time"

	"github.com/bsm/redislock"
	"github.com/thkhxm/tgf"
	"github.com/thkhxm/tgf/db"
	"github.com/thkhxm/tgf/rpc/internal"
)

// loginLockHandle 包装登录锁的释放句柄。生产实现是 *redislock.Lock，
// 测试 mock 用任意占位 struct。
type loginLockHandle interface {
	isLoginLock()
}

// loginCoordinator 是 Login 流程的副作用接口，独立于 gate.go 方便 mock。
type loginCoordinator interface {
	// AcquireLoginLock 获取 userId 的登录锁；带内部重试和短退避。
	// 失败返回 error；成功返回的 handle 必须被 ReleaseLoginLock 释放。
	AcquireLoginLock(userId string) (loginLockHandle, error)

	// ReleaseLoginLock 释放锁。即使锁已过期或释放过，调用也应是幂等的。
	ReleaseLoginLock(h loginLockHandle)

	// GetGateOwner 读取 user:node:meta hash 里的 gate owner（rpcx service address）。
	// 没有 owner 返回空串。
	GetGateOwner(userId string) string

	// SetGateOwner 把本节点写成 gate owner。
	SetGateOwner(userId, address string)

	// KickRemoteOwner 对 owner 节点定向发 Offline RPC。不等回执。
	// 注意：这里依赖 rpcx 的 "service@address" 路由能力，不走 BorderRPCMessage 广播。
	KickRemoteOwner(address, userId string) error
}

// loginCoord 是 loginCoordinator 的 package 单例。测试里可以临时替换它。
// phase2 初始化为默认实现；后续 C 档把它收敛到 Server builder 里注入。
var loginCoord loginCoordinator = &defaultLoginCoordinator{}

// defaultLoginCoordinator 是生产实现。
type defaultLoginCoordinator struct{}

// redislockHandle 是 loginLockHandle 对 *redislock.Lock 的包装。
type redislockHandle struct {
	lock *redislock.Lock
}

func (redislockHandle) isLoginLock() {}

const (
	// loginLockRetries 是 AcquireLoginLock 的最大尝试次数。
	// db.NewLock 内部 TTL 是 5 秒，本方允许的总等待时间 ≈ retries*backoff。
	loginLockRetries = 5
	loginLockBackoff = 100 * time.Millisecond
)

// remoteKickWait 是发送定向踢 RPC 后给远端的清理窗口。
// 远端 Offline 是异步的，这里短暂 sleep 让远端 Redis meta 释放；后续 A6
// 补 Consul health check + 远端 ack 后可以缩短或移除。
// 做成 var 而非 const 便于单测里缩短到 ~0。
var remoteKickWait = 200 * time.Millisecond

func (d *defaultLoginCoordinator) AcquireLoginLock(userId string) (loginLockHandle, error) {
	key := loginLockKey(userId)
	var lastErr error
	for i := 0; i < loginLockRetries; i++ {
		lock, err := db.NewLock(key)
		if err == nil {
			return redislockHandle{lock: lock}, nil
		}
		lastErr = err
		time.Sleep(loginLockBackoff)
	}
	return nil, fmt.Errorf("acquire login lock key=%s failed after %d retries: %w",
		key, loginLockRetries, lastErr)
}

func (d *defaultLoginCoordinator) ReleaseLoginLock(h loginLockHandle) {
	if h == nil {
		return
	}
	rh, ok := h.(redislockHandle)
	if !ok || rh.lock == nil {
		return
	}
	// db.UnLock 内部调 redislock.Lock.Release(context.Background())，
	// 对已过期 / 已释放的锁是幂等的（上层吞错）。
	db.UnLock(rh.lock)
}

func (d *defaultLoginCoordinator) GetGateOwner(userId string) string {
	meta, ok := db.GetMap[string, string](fmt.Sprintf(tgf.RedisKeyUserNodeMeta, userId))
	if !ok {
		return ""
	}
	return meta[tgf.GatewayServiceModuleName]
}

func (d *defaultLoginCoordinator) SetGateOwner(userId, address string) {
	db.PutMap(fmt.Sprintf(tgf.RedisKeyUserNodeMeta, userId),
		tgf.GatewayServiceModuleName, address, 0)
}

func (d *defaultLoginCoordinator) KickRemoteOwner(address, userId string) error {
	// SendNoReplyRPCMessageByAddress 走 rpcx 的定向调用，不等回执。
	return SendNoReplyRPCMessageByAddress(
		tgf.GatewayServiceModuleName,
		address,
		"Offline",
		&OfflineReq{UserId: userId, Replace: true},
	)
}

// loginLockKey 返回 Redis 登录锁的 key。独立出来方便 phase2 测试断言 key 格式，
// 也方便未来 B 档 metrics 按 key 前缀聚合埋点。
func loginLockKey(userId string) string {
	return "tgf:gate:login:lock:" + userId
}

// localGateAddress 返回本节点 gate 的 rpcx service address。
// 独立成函数方便测试里 mock（通过替换 globalLocalAddressFn）。
func localGateAddress() string {
	if globalLocalAddressFn != nil {
		return globalLocalAddressFn()
	}
	return internal.LocalServerAddress
}

// globalLocalAddressFn 是 localGateAddress 的测试钩子；非 nil 时覆盖默认实现。
var globalLocalAddressFn func() string
