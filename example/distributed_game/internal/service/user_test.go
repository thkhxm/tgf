package service

import (
	"testing"

	"context"
	"github.com/thkhxm/tgf/v2/rpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestUserServiceGetProfile(t *testing.T) {
	raw, err := proto.Marshal(wrapperspb.String("player_001"))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	reply := new(rpc.Reply[*wrapperspb.StringValue])
	if err := NewUserService().GetProfile(context.Background(), &rpc.Args[*wrapperspb.StringValue]{ByteData: raw}, reply); err != nil {
		t.Fatalf("GetProfile: %v", err)
	}

	got := new(wrapperspb.StringValue)
	if err := proto.Unmarshal(reply.ByteData, got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Value != "profile:player_001" || reply.Code != 0 {
		t.Fatalf("response = (%q, code=%d)", got.Value, reply.Code)
	}
}

func TestUserServiceGetProfileRejectsEmptyUser(t *testing.T) {
	raw, err := proto.Marshal(wrapperspb.String(""))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	reply := new(rpc.Reply[*wrapperspb.StringValue])
	err = NewUserService().GetProfile(context.Background(), &rpc.Args[*wrapperspb.StringValue]{ByteData: raw}, reply)
	if err == nil || reply.Code != 400 {
		t.Fatalf("empty user: err=%v code=%d", err, reply.Code)
	}
}
