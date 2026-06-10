package db

// TestMain 把进程级默认补偿队列（E4 起默认 File 实现）的落盘路径指到临时目录，
// 避免单测在仓库工作区残留 longevity_failures.log（任何触发落库失败且未注入
// 自定义队列的用例都会懒加载默认队列）。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tgf-db-test-*")
	if err == nil {
		defaultFailureQueuePath = filepath.Join(dir, "longevity_failures.log")
	}
	code := m.Run()
	defaultFailureQueueMu.Lock()
	if defaultFailureQueueInst != nil {
		_ = defaultFailureQueueInst.Close()
	}
	defaultFailureQueueMu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}

// swapDefaultFailureQueueForTest 把进程级默认补偿队列指到独立路径并在测试结束恢复，
// 隔离各用例对共享默认队列的读写。
func swapDefaultFailureQueueForTest(t *testing.T, path string) {
	t.Helper()
	defaultFailureQueueMu.Lock()
	oldInst, oldPath := defaultFailureQueueInst, defaultFailureQueuePath
	defaultFailureQueueInst, defaultFailureQueuePath = nil, path
	defaultFailureQueueMu.Unlock()
	t.Cleanup(func() {
		defaultFailureQueueMu.Lock()
		if defaultFailureQueueInst != nil {
			_ = defaultFailureQueueInst.Close()
		}
		defaultFailureQueueInst, defaultFailureQueuePath = oldInst, oldPath
		defaultFailureQueueMu.Unlock()
	})
}
