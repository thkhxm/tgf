package log

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//日志收集（直接发送、记录到本地再上传）、
//日志接收（消息队列，直接进入ElasticSearch）、
//数据清洗（Logstach、Storm、SparkStreaming）、
//日志存储（Mysql、Hbase、ElasticSearch）、
//页面展示（自研还是直接用第三方的）
//2024/2/21
//***************************************************

type Trace struct {
}

// SystemSpan 系统级别的span
type SystemSpan struct {
	Span
}

// GameSpan 游戏级别的span
type GameSpan struct {
	Span
	Tag string
}

// UserSpan 用户级别的span
type UserSpan struct {
	Span
	UserId string
}

type Span struct {
	TraceId    string // 跟踪id
	SourceNode string // 发起节点
	TargetNode string // 目标节点
	StartTime  int64  // 开始时间
}
