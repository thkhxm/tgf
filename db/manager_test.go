//go:build integration
// +build integration

package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description E5 重写：longevity 管理器真实链路集成测试。
//
// 原版（2023/3/16）的问题（V3 审计 P3，db/manager_test.go:49,293）：
//   - Test_autoCacheManager_Get 与 Test_hashAutoCacheManager_Set 以 `select {}`
//     收尾——测试永久挂死，整个 integration 套件从未跑完过；
//   - hash 用例依赖外部 MySQL 中预先手工塞好的 game_prop 行，不自包含；
//   - Push/Remove 用例是空的 TODO 占位。
//
// 现版本：
//   - 真实链路（容器 Redis+MySQL）的自包含用例收敛在 it_writebehind_test.go
//     （Set/Push/Remove/GetAll/落库/回读/删除不复活全覆盖，全部有界）；
//   - 本文件保留并修复原有的纯函数用例，并补一条"多 key 组合主键"的真实
//     回读用例（原 hash 用例想测但从未测成的场景）。
//
//2023/3/16（E5 重写 2026/6/10）
//***************************************************

import (
	"testing"
	"time"
)

func Test_convertCamelToSnake(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "UserId", arg: "UserId", want: "`user_id`"},
		{name: "State", arg: "State", want: "`state`"},
		{name: "Id", arg: "Id", want: "`id`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertCamelToSnake(tt.arg); got != tt.want {
				t.Errorf("ConvertCamelToSnake(%v) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

// TestIT_HashManager_CompositePKGet 复合主键（user_id + prop_id）的写入与
// 跨管理器真实回读——替代原 Test_hashAutoCacheManager_Get 依赖手工预置数据的写法。
func TestIT_HashManager_CompositePKGet(t *testing.T) {
	env := ensureIT(t)

	newHash := func() IHashCacheService[*itItem] {
		return NewHashAutoCacheBuilder[*itItem]().
			WithLongevityCache(time.Second).
			WithMemCache(300).
			New()
	}

	h := newHash()
	want := &itItem{Model: NewModel(), UserId: "u-pk", PropId: "2", Amount: 1}
	if ok := h.Set(want, "u-pk", "2"); !ok {
		t.Fatal("Set 失败")
	}
	waitUntil(t, 10*time.Second, "复合主键行落库", func() bool {
		amount, state, ok := env.queryItemState("u-pk", "2")
		return ok && amount == 1 && state == 1
	})

	// 全新管理器按复合 key 真实回读（queryList → 本地缓存 → Get）。
	h2 := newHash()
	got, err := h2.Get("u-pk", "2")
	if err != nil {
		t.Fatalf("Get(u-pk,2) 回读失败: %v", err)
	}
	if got.UserId != "u-pk" || got.PropId != "2" || got.Amount != 1 {
		t.Fatalf("回读数据不符: %+v, want %+v", got, want)
	}
}
