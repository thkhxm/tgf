package main

import (
	"strings"
	"testing"

	"context"
)

func TestPlayerService(t *testing.T) {
	service := NewPlayerService()
	profile := new(GetPlayerRes)
	if err := service.GetPlayer(context.Background(), &GetPlayerReq{PlayerId: "p001"}, profile); err != nil {
		t.Fatalf("GetPlayer: %v", err)
	}
	if profile.PlayerId != "p001" || profile.Level != 10 || profile.Coins != 9999 {
		t.Fatalf("profile=%+v", profile)
	}

	gift := new(GiveGiftRes)
	if err := service.GiveGift(context.Background(), &GiveGiftReq{PlayerId: "p001", GiftId: "welcome"}, gift); err != nil {
		t.Fatalf("GiveGift: %v", err)
	}
	if !gift.OK || !strings.Contains(gift.Message, "p001") || !strings.Contains(gift.Message, "welcome") {
		t.Fatalf("gift=%+v", gift)
	}
}
