// tgf v2 示例：component 包 — 游戏配置加载与热更
//
// 演示 InitGameConfToMem / GetGameConf / ReloadGameConf / OnReload / StartConfigWatcher。
// 会在临时目录创建 JSON 文件来模拟游戏配置。
//
// 运行：cd tgf/example/game_config && go run .
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/thkhxm/tgf/component"
	"github.com/thkhxm/tgf/log"
)

// ====================================================================
// 1. 定义配置结构体
// ====================================================================

// HeroConf 英雄配置。
// 约定：
//   - 第一个字段必须是 ID（框架用 reflect.Field(0) 取主键）
//   - 类型名 "HeroConf" 对应文件名 "Hero.json"（文件名去 .json + "Conf" = 类型名）
type HeroConf struct {
	Id     string
	Name   string
	Attack int
	HP     int
}

// ItemConf 道具配置。
type ItemConf struct {
	Id    string
	Name  string
	Price int
}

func main() {
	fmt.Println("=== tgf v2 示例：游戏配置热更 ===")
	fmt.Println()

	// ----------------------------------------------------------------
	// 准备：创建临时目录 + 写入测试 JSON
	// ----------------------------------------------------------------
	dir, _ := os.MkdirTemp("", "tgf-gameconf-*")
	defer os.RemoveAll(dir)

	// Hero.json → key "HeroConf"
	writeJSON(dir, "Hero.json", `[
		{"Id":"h001","Name":"Arthur","Attack":100,"HP":1000},
		{"Id":"h002","Name":"Lancelot","Attack":120,"HP":800},
		{"Id":"h003","Name":"Galahad","Attack":80,"HP":1200}
	]`)

	// Item.json → key "ItemConf"
	writeJSON(dir, "Item.json", `[
		{"Id":"i001","Name":"Iron Sword","Price":100},
		{"Id":"i002","Name":"Health Potion","Price":50}
	]`)

	// ----------------------------------------------------------------
	// 1. 初始化
	// ----------------------------------------------------------------
	fmt.Println("--- 1. 初始化 ---")
	component.WithConfPath(dir)
	component.InitGameConfToMem()
	fmt.Println()

	// ----------------------------------------------------------------
	// 2. GetGameConf — 按 ID 查询单条
	// ----------------------------------------------------------------
	fmt.Println("--- 2. GetGameConf 按 ID 查询 ---")

	if hero, ok := component.GetGameConf[*HeroConf]("h001"); ok {
		fmt.Printf("  h001: %s ATK=%d HP=%d\n", hero.Name, hero.Attack, hero.HP)
	}
	if hero, ok := component.GetGameConf[*HeroConf]("h002"); ok {
		fmt.Printf("  h002: %s ATK=%d HP=%d\n", hero.Name, hero.Attack, hero.HP)
	}
	if _, ok := component.GetGameConf[*HeroConf]("h999"); !ok {
		fmt.Println("  h999: 不存在")
	}
	fmt.Println()

	// ----------------------------------------------------------------
	// 3. GetAllGameConf — 获取全部
	// ----------------------------------------------------------------
	fmt.Println("--- 3. GetAllGameConf ---")

	allItems := component.GetAllGameConf[*ItemConf]()
	for _, item := range allItems {
		fmt.Printf("  %s: %s 价格=%d\n", item.Id, item.Name, item.Price)
	}
	fmt.Println()

	// ----------------------------------------------------------------
	// 4. RangeGameConf — 遍历（推荐用法）
	// ----------------------------------------------------------------
	fmt.Println("--- 4. RangeGameConf 遍历 ---")

	component.RangeGameConf[*HeroConf](func(id string, hero *HeroConf) bool {
		fmt.Printf("  [Range] id=%s name=%s\n", id, hero.Name)
		return true // 返回 false 中断遍历
	})
	fmt.Println()

	// ----------------------------------------------------------------
	// 5. OnReload — 注册热更回调
	// ----------------------------------------------------------------
	fmt.Println("--- 5. 注册 OnReload 回调 ---")

	component.OnReload(func() {
		fmt.Println("  [OnReload] 游戏配置已热更！")
		// 这里可以清业务侧的派生缓存
	})
	fmt.Println()

	// ----------------------------------------------------------------
	// 6. ReloadGameConf — 手动触发热更
	// ----------------------------------------------------------------
	fmt.Println("--- 6. 手动热更 ---")

	// 修改 Hero.json
	writeJSON(dir, "Hero.json", `[
		{"Id":"h001","Name":"Arthur (强化版)","Attack":200,"HP":2000},
		{"Id":"h002","Name":"Lancelot","Attack":120,"HP":800},
		{"Id":"h004","Name":"Percival","Attack":90,"HP":1100}
	]`)

	if err := component.ReloadGameConf(); err != nil {
		fmt.Printf("  热更失败: %v\n", err)
	}

	// 读取新数据
	if hero, ok := component.GetGameConf[*HeroConf]("h001"); ok {
		fmt.Printf("  h001 热更后: %s ATK=%d HP=%d\n", hero.Name, hero.Attack, hero.HP)
	}
	if hero, ok := component.GetGameConf[*HeroConf]("h004"); ok {
		fmt.Printf("  h004 新增: %s ATK=%d HP=%d\n", hero.Name, hero.Attack, hero.HP)
	}
	fmt.Println()

	// ----------------------------------------------------------------
	// 7. StartConfigWatcher — 文件监听自动热更
	// ----------------------------------------------------------------
	fmt.Println("--- 7. 文件监听 ---")

	component.OnReload(func() {
		fmt.Println("  [Watcher OnReload] 检测到文件变化，已自动热更！")
	})

	if err := component.StartConfigWatcher(); err != nil {
		fmt.Printf("  启动 watcher 失败: %v\n", err)
	} else {
		fmt.Println("  watcher 已启动，修改 JSON 文件会自动触发 ReloadGameConf")

		// 模拟修改文件
		time.Sleep(100 * time.Millisecond)
		writeJSON(dir, "Hero.json", `[
			{"Id":"h001","Name":"Arthur (觉醒)","Attack":500,"HP":5000}
		]`)

		// 等待 watcher debounce + reload
		time.Sleep(500 * time.Millisecond)

		if hero, ok := component.GetGameConf[*HeroConf]("h001"); ok {
			fmt.Printf("  watcher 触发后 h001: %s ATK=%d\n", hero.Name, hero.Attack)
		}

		component.StopConfigWatcher()
		fmt.Println("  watcher 已停止")
	}
	fmt.Println()

	// ----------------------------------------------------------------
	// 8. GetGameConfBySlice — 同 ID 多条记录
	// ----------------------------------------------------------------
	fmt.Println("--- 8. GetGameConfBySlice ---")
	fmt.Println("  适用于同一 ID 有多条记录的场景（如同一英雄多个技能配置）")
	fmt.Println()

	// ----------------------------------------------------------------
	// 9. 配置结构体约定
	// ----------------------------------------------------------------
	fmt.Println("--- 9. 配置结构体约定 ---")
	fmt.Println(`
  // 1. 文件名和类型名的对应关系
  //    文件名 "Hero.json" → key = "Hero" + "Conf" = "HeroConf"
  //    类型名必须是 HeroConf 才能匹配

  // 2. 第一个字段必须是 ID（string）
  type HeroConf struct {
      Id     string    // ← 主键，框架用 reflect.Field(0) 读取
      Name   string
      Attack int
      HP     int
  }

  // 3. JSON 文件格式：数组
  // [
  //   {"Id":"h001","Name":"Arthur","Attack":100,"HP":1000},
  //   {"Id":"h002","Name":"Lancelot","Attack":120,"HP":800}
  // ]
  `)

	fmt.Println("=== 游戏配置热更示例结束 ===")
}

func writeJSON(dir, filename, content string) {
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.Error("写 JSON 失败: %v", err)
	}
}
