package main

import (
	"testing"

	"context"
)

func TestShopServiceBusinessHandlers(t *testing.T) {
	service := NewShopService()
	gift := new(GiveGiftRes)
	if err := service.GiveGift(context.Background(), &GiveGiftReq{UserId: "p001", GiftId: "welcome"}, gift); err != nil {
		t.Fatalf("GiveGift: %v", err)
	}
	if !gift.OK {
		t.Fatalf("gift=%+v", gift)
	}

	buy := new(BuyItemRes)
	if err := service.BuyItem(context.Background(), &BuyItemReq{UserId: "p001", ItemId: "sword"}, buy); err != nil {
		t.Fatalf("BuyItem: %v", err)
	}
	if !buy.Success {
		t.Fatalf("buy=%+v", buy)
	}
}
