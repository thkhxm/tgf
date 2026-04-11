package tgf

import "errors"

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/3/16
//***************************************************

type GameError interface {
	Error() string
	Code() int32
}

var (
	ErrorRPCTimeOut = errors.New("rpc time out")
	LocalEmpty      = errors.New("local cache empty")
	RedisEmpty      = errors.New("redis cache empty")
	DBEmpty         = errors.New("db cache empty")
	ServiceNotFound = errors.New("service not found")

	// A2-phase3 网关推送相关错误：
	// ErrConnClosed - 调用 Send/ToUser 时连接已被 Offline
	// ErrConnSendTimeout - writeChan 推入超过 defaultSendChanTimeout 仍未被 writer 消费
	// ErrUserNotFound - ToUser 目标用户在本节点不在线
	ErrConnClosed      = errors.New("tgf/rpc: connection already closed")
	ErrConnSendTimeout = errors.New("tgf/rpc: send chan timeout")
	ErrUserNotFound    = errors.New("tgf/rpc: user connection not found")
)
