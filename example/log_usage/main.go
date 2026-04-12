// tgf v2 示例：log 包 — 日志系统
//
// 演示老 API（Sprintf 风格）和新 API（zap.Field 风格）的使用方式。
// 以及 tag 过滤、SugaredLogger 直接访问等用法。
//
// 运行：cd tgf/example/log_usage && go run .
package main

import (
	"fmt"
	"time"

	"github.com/thkhxm/tgf/log"
	"go.uber.org/zap"
)

func main() {
	fmt.Println("=== tgf v2 示例：log 包 ===")
	fmt.Println()

	// ----------------------------------------------------------------
	// 1. 基础日志（Sprintf 风格，v1 兼容）
	// ----------------------------------------------------------------
	fmt.Println("--- 1. Sprintf 风格 ---")

	log.Info("服务启动成功 port=%d", 8082)
	log.Debug("调试信息 key=%s val=%v", "foo", 42)
	log.Warn("警告: 连接池快满了 used=%d/%d", 95, 100)
	log.Error("错误: 数据库超时 err=%v", fmt.Errorf("timeout"))
	fmt.Println()

	// ----------------------------------------------------------------
	// 2. 带 Tag 的日志（Tag 可被 LogIgnoredTags 过滤）
	// ----------------------------------------------------------------
	fmt.Println("--- 2. Tag 过滤 ---")

	log.InfoTag("gate", "新连接 addr=%s", "192.168.1.100:5000")
	log.DebugTag("db", "查询耗时 table=%s ms=%d", "t_user", 3)
	log.WarnTag("rpc", "RPC 超时 module=%s method=%s", "shop", "BuyItem")
	log.ErrorTag("auth", "登录校验失败 userId=%s", "u001")

	// CheckLogTag 可以在业务代码里做分支判断
	if log.CheckLogTag("gate") {
		// 只在 gate tag 未被过滤时才执行（避免昂贵的参数构造）
		log.InfoTag("gate", "gate tag 允许通过")
	}
	fmt.Println()

	// ----------------------------------------------------------------
	// 3. zap.Field 风格 — v2 推荐（零 Sprintf 分配）
	// ----------------------------------------------------------------
	fmt.Println("--- 3. zap.Field 风格（v2 推荐）---")

	log.InfoW("连接接入",
		zap.String("addr", "10.0.0.1:5000"),
		zap.Int("fd", 42),
	)

	log.DebugW("帧处理完成",
		zap.String("module", "gate"),
		zap.Duration("latency", 3*time.Millisecond),
		zap.Int64("frameId", 100001),
	)

	log.WarnW("缓存未命中",
		zap.String("key", "user:u001:profile"),
		zap.Bool("fallback", true),
	)

	log.ErrorW("数据库错误",
		zap.Error(fmt.Errorf("connection refused")),
		zap.String("table", "t_order"),
	)
	fmt.Println()

	// ----------------------------------------------------------------
	// 4. 带 Tag 的 zap.Field 风格
	// ----------------------------------------------------------------
	fmt.Println("--- 4. Tag + zap.Field ---")

	log.InfoTagW("gate", "连接建立",
		zap.String("addr", "10.0.0.5:6000"),
		zap.String("transport", "tcp"),
	)

	log.DebugTagW("orm", "SQL 执行",
		zap.String("sql", "SELECT * FROM t_user WHERE uid=?"),
		zap.Int("rows", 1),
		zap.Duration("elapsed", 2*time.Millisecond),
	)

	log.WarnTagW("rpc", "限流触发",
		zap.String("method", "shop.BuyItem"),
		zap.Float64("qps", 150.5),
	)

	log.ErrorTagW("security", "非法请求",
		zap.String("ip", "1.2.3.4"),
		zap.Int("code", 403),
	)
	fmt.Println()

	// ----------------------------------------------------------------
	// 5. 特殊用途日志
	// ----------------------------------------------------------------
	fmt.Println("--- 5. 特殊日志函数 ---")

	// 游戏事件日志（自动写到独立的 game log 文件）
	log.Game("u001", "battle", "战斗结算 damage=%d", 500)

	// 数据库日志（自动写到独立的 db log 文件）
	log.DB("trace-001", "tgf", "INSERT INTO t_user VALUES (...)", 1)

	// 服务调用日志（自动写到独立的 service log 文件）
	log.Service("shop", "BuyItem", "1.0", "u001", 15, 0)
	fmt.Println()

	// ----------------------------------------------------------------
	// 6. 直接用 SugaredLogger
	// ----------------------------------------------------------------
	fmt.Println("--- 6. SugaredLogger ---")

	sugar := log.SLogger()
	sugar.Infow("sugar logger 示例",
		"module", "example",
		"count", 3,
	)
	sugar.Debugf("sugar debug: %d + %d = %d", 1, 2, 3)
	fmt.Println()

	// ----------------------------------------------------------------
	// 7. 最佳实践对比
	// ----------------------------------------------------------------
	fmt.Println("--- 7. 最佳实践 ---")
	bestPractice := "" +
		"  // v1 风格（会 Sprintf 分配）\n" +
		"  log.DebugTag(\"gate\", \"帧处理 addr=%%s latency=%%v\", addr, elapsed)\n" +
		"\n" +
		"  // v2 推荐（零分配，level 过滤时完全跳过）\n" +
		"  log.DebugTagW(\"gate\", \"帧处理\",\n" +
		"      zap.String(\"addr\", addr),\n" +
		"      zap.Duration(\"latency\", elapsed),\n" +
		"  )\n" +
		"\n" +
		"  // 热路径外层做 tag 检查（避免构造昂贵参数）\n" +
		"  if log.CheckLogTag(\"expensive\") {\n" +
		"      data := buildExpensiveDebugData()\n" +
		"      log.DebugTag(\"expensive\", \"data=%%v\", data)\n" +
		"  }\n"
	fmt.Println(bestPractice)

	fmt.Println("=== log 示例结束 ===")
}
