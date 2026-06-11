package rpc

// E 档配置读点迁移后的测试公共设施。
//
// 背景：rpc 包的凭据/参数读点已从 os.Getenv 直读迁移到 tgfconfig.Current()
// 快照（启动时由 tgf.InitConfig 解析）。测试里修改进程环境变量后必须显式
// Reload 才会进入快照。
//
// 为什么不能用 `t.Setenv + 单独的 reload helper`：t.Cleanup 是 LIFO——先调
// t.Setenv（注册还原 env 的 cleanup）再注册"还原快照"的 cleanup，会导致快照
// 还原先于 env 还原执行，测试值残留进快照污染后续测试。本 helper 把 env 还原
// 与快照重建收进同一个 cleanup（先还原 env 再 Reload），顺序自洽。

import (
	"os"
	"testing"

	tgfconfig "github.com/thkhxm/tgf/v2/config"
)

// setEnvConfigForTest 设置环境变量并立即重建配置快照；测试结束时还原 env
// 并再次重建快照（保证测试间零残留）。等价于"t.Setenv + Reload"，但 cleanup
// 顺序正确。不可与 t.Parallel 并用（与 t.Setenv 同约束）。
func setEnvConfigForTest(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
		_, _ = tgfconfig.Reload()
	})
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s: %v", key, err)
	}
	if _, err := tgfconfig.Reload(); err != nil {
		t.Fatalf("config reload: %v", err)
	}
}
