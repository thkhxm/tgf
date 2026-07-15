// Package service 放置分布式游戏服的业务实现。
package service

import (
	"fmt"

	"context"
	"github.com/thkhxm/tgf/v2/example/distributed_game/internal/api"
	"github.com/thkhxm/tgf/v2/rpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type UserService struct {
	rpc.Module
}

func NewUserService() *UserService {
	return &UserService{Module: rpc.Module{Name: api.UserModule, Version: "1.0"}}
}

func (s *UserService) Startup() (bool, error) { return true, nil }

// GetProfile 是可从 TCP/WS/KCP 网关转发到业务进程的 handler。
func (s *UserService) GetProfile(_ context.Context, args *rpc.Args[*wrapperspb.StringValue], reply *rpc.Reply[*wrapperspb.StringValue]) error {
	request := args.GetData()
	if request == nil || request.Value == "" {
		reply.SetCode(400)
		return fmt.Errorf("user id is required")
	}
	if err := reply.SetData(wrapperspb.String("profile:" + request.Value)); err != nil {
		return err
	}
	reply.SetCode(0)
	return nil
}
