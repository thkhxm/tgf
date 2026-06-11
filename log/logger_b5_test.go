package log_test

// B5: 日志热路径优化的回归测试。
// 目标：验证
//   1. 老的 `*` / `*Tag` API 在 level/tag 被过滤时完全跳过 Sprintf（不 panic，不分配）
//   2. 新的 `*W` / `*TagW` API 可以处理 zap.Field
//   3. 老 API 的调用不会因为 B5 改造退化（基本 smoke test）
//
// 注：B5 优化"level 过滤时跳过 Sprintf"的行为级断言见 logger_e3_test.go 的
// TestTagWT_LevelFilterSkipsFieldConstruction / TestAtomicLevel_RuntimeReload
// （白盒 + observer core，弥补了 V3 审计指出的"B5 测试全是 smoke 无断言"缺口）。

import (
	"testing"

	"github.com/thkhxm/tgf/v2/log"
	"go.uber.org/zap"
)

// TestLoggerW_SmokeNoCrash 只验证新 API 不 panic。
// 真正的输出断言需要一个 in-memory zap core，而 tgf.log.initLogger 是包级单例，
// 改造它支持注入 core 是另一项工作（留给 C 档或 observability 迭代）。
func TestLoggerW_SmokeNoCrash(t *testing.T) {
	log.InfoW("info w", zap.String("k", "v"))
	log.DebugW("debug w", zap.Int("n", 1))
	log.WarnW("warn w", zap.Float64("f", 1.5))
	log.ErrorW("error w", zap.Bool("b", true))

	log.InfoTagW("b5", "info tag w", zap.String("k", "v"))
	log.DebugTagW("b5", "debug tag w", zap.Int("n", 1))
	log.WarnTagW("b5", "warn tag w", zap.Float64("f", 1.5))
	log.ErrorTagW("b5", "error tag w", zap.Bool("b", true))
}

// TestLoggerTagFiltering_DoesNotCrash 验证 tag 过滤路径不 panic。
// 因为 initLogger 里 ignoredTags 由环境变量决定，这里只能保证"未过滤 tag"
// 的调用本身不 panic——实际过滤逻辑的覆盖率在 CheckLogTag 单测里。
func TestLoggerTagFiltering_DoesNotCrash(t *testing.T) {
	for i := 0; i < 5; i++ {
		log.InfoTag("b5-smoke", "iter=%d", i)
		log.DebugTag("b5-smoke", "iter=%d", i)
		log.WarnTag("b5-smoke", "iter=%d", i)
		log.ErrorTag("b5-smoke", "iter=%d", i)
	}
}

// TestCheckLogTag_IgnoredMap 验证 CheckLogTag 的返回值符合预期。
// ignoredTags 是包私有 map，这里只能走"没设置 ignored"的默认路径测试。
func TestCheckLogTag_DefaultAllow(t *testing.T) {
	if !log.CheckLogTag("any-tag") {
		t.Fatal("默认所有 tag 都应被放行")
	}
}

// BenchmarkInfoTag_Filtered 粗略度量 B5 前后的差异。运行方式：
//
//	go test ./log -bench=BenchmarkInfoTag -benchmem
//
// 注意：这个 bench 只衡量"level 未命中时的开销"，因为测试环境 ignoredTags 为空。
// 真正在 WARN 级别下跑时，老实现会执行 Sprintf，新实现完全跳过。
func BenchmarkInfoTag_LevelPassthrough(b *testing.B) {
	for i := 0; i < b.N; i++ {
		log.InfoTag("bench", "tick=%d", i)
	}
}

func BenchmarkInfoTagW_LevelPassthrough(b *testing.B) {
	for i := 0; i < b.N; i++ {
		log.InfoTagW("bench", "tick", zap.Int("i", i))
	}
}
