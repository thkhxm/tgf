package main

import (
	"testing"

	"context"
	"github.com/thkhxm/tgf/v2/rpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestGameServiceGetRole(t *testing.T) {
	raw, err := proto.Marshal(wrapperspb.String("unit_player"))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	reply := new(rpc.Reply[*wrapperspb.StringValue])
	service := NewGameService()
	if err := service.GetRole(context.Background(), &rpc.Args[*wrapperspb.StringValue]{ByteData: raw}, reply); err != nil {
		t.Fatalf("GetRole: %v", err)
	}

	got := new(wrapperspb.StringValue)
	if err := proto.Unmarshal(reply.ByteData, got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Value != "Hero_unit_player" || reply.Code != 0 {
		t.Fatalf("response = (%q, code=%d), want (Hero_unit_player, 0)", got.Value, reply.Code)
	}
}
