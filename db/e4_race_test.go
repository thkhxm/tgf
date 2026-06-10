package db

// E4 · 并发正确性回归（须配合 -race 运行）：
//  1. cacheData.clearTime 原子化——读路径 getData（滑动续期写）与 autoClear 的
//     checkTimeOut 读不再构成 data race（B6 漏修）
//  2. toLongevity lost-update 竞态——flush 窗口内的并发修改不会被清脏标志吞掉
//     （确定性用例 + 并发压力用例）

import (
	"sync"
	"testing"
	"time"
)

// 1. clearTime / state 并发读写在 -race 下干净
func TestCacheData_ConcurrentAccessRaceClean(t *testing.T) {
	cd := newCacheData[*fakeModel](&fakeModel{Uid: "u"}, 10)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				cd.getData(10) // 滑动续期：写 clearTime
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				cd.checkTimeOut(time.Now().Unix()) // 读 clearTime
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				cd.update()
				cd.removeState(data_update)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				cd.checkState(data_del)
				cd.del(5)
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// 2a. 确定性 lost-update 回归：flush 窗口内（快照已取、清脏未执行）发生并发修改，
// 脏标志必须保留——这次修改要等下一轮才落库，但绝不能丢。
func TestFlushDirty_ConcurrentModifyKeepsDirtyFlag(t *testing.T) {
	m := newTestManager(10)
	items := seedDirty(m, 1)

	m.flushBatch = func(values []any, count int) error {
		// 模拟业务在 flush 进行中（旧值快照已取出）原地修改数据并 Push
		items[0].update()
		return nil
	}
	m.toLongevity()

	if !items[0].checkState(data_update) {
		t.Fatalf("flush 窗口内的并发修改被无条件清脏吞掉——E4 lost-update 竞态回归")
	}

	// 下一轮（无并发修改）应正常清脏
	m.flushBatch = func(values []any, count int) error { return nil }
	m.toLongevity()
	if items[0].checkState(data_update) {
		t.Fatalf("第二轮 flush 后脏标志应被清除")
	}
}

// 2b. 并发压力：多个 goroutine 持续 update() 的同时反复 flush，-race 必须干净，
// 且停止修改后最后一轮 flush 必然能把所有条目清脏（无永久卡死的脏标志）。
func TestFlushDirty_ConcurrentUpdateStress(t *testing.T) {
	m := newTestManager(50)
	items := seedDirty(m, 64)
	m.flushBatch = func(values []any, count int) error { return nil }

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(4)
	for g := 0; g < 4; g++ {
		go func(offset int) {
			defer wg.Done()
			i := offset
			for {
				select {
				case <-stop:
					return
				default:
					items[i%len(items)].update()
					i++
				}
			}
		}(g * 16)
	}

	for i := 0; i < 20; i++ {
		m.toLongevity()
	}
	close(stop)
	wg.Wait()

	// 修改停止后，最终一轮 flush 应能把全部条目清脏
	m.toLongevity()
	for i, cd := range items {
		if cd.checkState(data_update) {
			t.Errorf("修改停止后 item %d 的脏标志应在最终 flush 被清除", i)
		}
	}
}
