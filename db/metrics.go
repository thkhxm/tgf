package db

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description E3-db · 数据层关键路径埋点
//
// 复用框架 metrics 接口（tgf/metrics，B4 档落地）。Provider 默认 NoOp 零开销；
// 业务在 Server 启动前 metrics.SetProvider(prometheus adapter) 后点位自动生效。
//
// 指标点位：
//   tgf_db_flush_batch_total           Counter  落库批次总数（含失败批次）
//   tgf_db_flush_batch_fail_total      Counter  重试耗尽仍失败的批次数
//   tgf_db_flush_rows_success_total    Counter  成功落库的条数
//   tgf_db_flush_rows_fail_total       Counter  失败批次包含的条数
//   tgf_db_failure_queue_enqueue_total Counter  写入补偿队列的批次数
//   tgf_db_failure_queue_depth         Gauge    补偿队列当前深度
//   tgf_db_failure_replay_total        Counter  replay 成功重放的 payload 数
//   tgf_db_failure_replay_fail_total   Counter  replay 失败（重新入队）的 payload 数
//
// 实现取舍：每次取指标都走 metrics.NewCounter / NewGauge（即 GetProvider().XXX），
// 不做包级缓存引用。flush 周期默认 5s、replay 仅启动期执行，均非热路径；
// 换取的是"SetProvider 之后埋点立即生效"（memoryProvider / prometheus adapter
// 对同名指标返回同一实例，不会重复注册），单测无需关心初始化顺序。
//
//2026/6/10
//***************************************************

import "github.com/thkhxm/tgf/v2/metrics"

const (
	metricDBFlushBatchTotal       = "tgf_db_flush_batch_total"
	metricDBFlushBatchFailTotal   = "tgf_db_flush_batch_fail_total"
	metricDBFlushRowsSuccessTotal = "tgf_db_flush_rows_success_total"
	metricDBFlushRowsFailTotal    = "tgf_db_flush_rows_fail_total"
	metricDBFailureEnqueueTotal   = "tgf_db_failure_queue_enqueue_total"
	metricDBFailureQueueDepth     = "tgf_db_failure_queue_depth"
	metricDBFailureReplayTotal    = "tgf_db_failure_replay_total"
	metricDBFailureReplayFail     = "tgf_db_failure_replay_fail_total"
)

func mcFlushBatchTotal() metrics.Counter {
	return metrics.NewCounter(metricDBFlushBatchTotal, "落库批次总数(含失败批次)")
}

func mcFlushBatchFailTotal() metrics.Counter {
	return metrics.NewCounter(metricDBFlushBatchFailTotal, "重试耗尽仍失败的落库批次数")
}

func mcFlushRowsSuccessTotal() metrics.Counter {
	return metrics.NewCounter(metricDBFlushRowsSuccessTotal, "成功落库的条数")
}

func mcFlushRowsFailTotal() metrics.Counter {
	return metrics.NewCounter(metricDBFlushRowsFailTotal, "失败批次包含的条数")
}

func mcFailureEnqueueTotal() metrics.Counter {
	return metrics.NewCounter(metricDBFailureEnqueueTotal, "写入补偿队列的批次数")
}

func mgFailureQueueDepth() metrics.Gauge {
	return metrics.NewGauge(metricDBFailureQueueDepth, "补偿队列当前深度")
}

func mcFailureReplayTotal() metrics.Counter {
	return metrics.NewCounter(metricDBFailureReplayTotal, "补偿队列 replay 成功重放的 payload 数")
}

func mcFailureReplayFailTotal() metrics.Counter {
	return metrics.NewCounter(metricDBFailureReplayFail, "补偿队列 replay 失败(重新入队)的 payload 数")
}
