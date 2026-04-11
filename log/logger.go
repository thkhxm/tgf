package log

import (
	"fmt"
	"github.com/thkhxm/tgf"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//***************************************************
//@Link  https://github.com/thkhxm/tgf
//@Link  https://gitee.com/timgame/tgf
//@QQ群 7400585
//author tim.huang<thkhxm@gmail.com>
//@Description
//2023/2/22
//***************************************************

var logger *zap.Logger
var slogger *zap.SugaredLogger

var (
	defaultMaxSize    = 512
	defaultMaxAge     = 0
	defaultMaxBackups = 100
	ignoredTags       map[string]bool
)

const (
	GAMETAG    = "game"
	DBTAG      = "db"
	SERVICETAG = "service"
)

// B5 说明 — 日志热路径的两条优化原则：
//
//  1. 所有 `*Tag` / `*` 家族在执行 `fmt.Sprintf` 之前，先用 `logger.Check(level, "")`
//     过一遍 zap 的 level/sampling 过滤器。当 LogLevel 被设置为 WARN 时，所有
//     `DebugTag` / `InfoTag` 调用的 Sprintf 会被跳过——这是最大的热路径收益点。
//     之前版本只做了 tag 过滤，tag 未过滤时仍然会 Sprintf 再让 zap 丢弃。
//
//  2. 新代码建议直接用 `*W` 后缀的 zap.Field 版本（`InfoTagW` / `DebugTagW` 等）。
//     这些函数完全不触碰 `fmt.Sprintf`，调用方手写 `zap.String("k", v)` 避免
//     interface{} 装箱。老的 `InfoTag(...)` 式 API 会保留到 v2 发布后一版，
//     在 CHANGELOG 标注 deprecated。

// checkTag 是 tag 过滤 + level 过滤的统一入口。返回值：
//   - nil 表示消息应被完全丢弃（tag 过滤 / level 过滤命中任一）
//   - 非 nil 的 CheckedEntry 表示应当执行 Sprintf 并调 Write
//
// 这个函数是 B5 的热路径，必须零分配：所有分支都走 pointer 比较和 map 读取，
// 不做 interface 装箱、不分配闭包。
func checkTag(level zapcore.Level, tag string) *zapcore.CheckedEntry {
	// tag 过滤放在 level 过滤前——map 读取比 zap 的 level 比较稍贵一点，但
	// 实际上 ignoredTags 命中率很低，而 level 过滤命中率可能很高；调换顺序
	// 在 "log level WARN、DebugTag 调用" 的最常见场景下可以少一次 map lookup。
	// 不过为了保持语义清晰（先判断 tag，再让 zap 判断 level），这里还是
	// tag → level 的顺序。如果未来 profiling 显示这是瓶颈，再调换。
	if ignoredTags != nil && ignoredTags[tag] {
		return nil
	}
	return logger.Check(level, "")
}

// ---- Sprintf 风格的老 API（带 level + tag 双重前置检查） ----

func Info(msg string, params ...interface{}) {
	if ce := logger.Check(zapcore.InfoLevel, ""); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write()
	}
}

func InfoTag(tag string, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.InfoLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag))
	}
}

func Debug(msg string, params ...interface{}) {
	if ce := logger.Check(zapcore.DebugLevel, ""); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write()
	}
}

func DebugTag(tag string, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.DebugLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag))
	}
}

func Warn(msg string, params ...interface{}) {
	if ce := logger.Check(zapcore.WarnLevel, ""); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write()
	}
}

func WarnTag(tag string, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.WarnLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag))
	}
}

func Error(msg string, params ...interface{}) {
	if ce := logger.Check(zapcore.ErrorLevel, ""); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write()
	}
}

func ErrorTag(tag string, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.ErrorLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag))
	}
}

// ---- zap.Field 风格的新 API（零分配热路径） ----

// InfoW / DebugW / WarnW / ErrorW 是对 `zap.Logger.Info(msg, fields...)` 的直通，
// 避免 Sprintf 分配。msg 固定字符串，字段用 zap.String / zap.Int 等手写传入。
//
// 示例：
//
//	log.DebugW("接收到连接", zap.String("addr", addr), zap.Int("fd", fd))
func InfoW(msg string, fields ...zap.Field)  { logger.Info(msg, fields...) }
func DebugW(msg string, fields ...zap.Field) { logger.Debug(msg, fields...) }
func WarnW(msg string, fields ...zap.Field)  { logger.Warn(msg, fields...) }
func ErrorW(msg string, fields ...zap.Field) { logger.Error(msg, fields...) }

// InfoTagW 等系列带 tag 过滤。tag 命中 ignoredTags 时完全短路。
func InfoTagW(tag, msg string, fields ...zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	logger.Info(msg, append(fields, zap.String("tag", tag))...)
}

func DebugTagW(tag, msg string, fields ...zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	logger.Debug(msg, append(fields, zap.String("tag", tag))...)
}

func WarnTagW(tag, msg string, fields ...zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	logger.Warn(msg, append(fields, zap.String("tag", tag))...)
}

func ErrorTagW(tag, msg string, fields ...zap.Field) {
	if ignoredTags != nil && ignoredTags[tag] {
		return
	}
	logger.Error(msg, append(fields, zap.String("tag", tag))...)
}

// ---- 其它现有 API（保持不变，都是结构化字段调用，没 Sprintf 问题） ----

func SLogger() *zap.SugaredLogger {
	return slogger
}

func Game(userId, tag, msg string, params ...interface{}) {
	if ce := checkTag(zapcore.InfoLevel, tag); ce != nil {
		ce.Message = fmt.Sprintf(msg, params...)
		ce.Write(zap.String("tag", tag), zap.String("userId", userId))
	}
}

func DB(traceId, dbName, script string, count int32) {
	logger.Debug(script, zap.String("tag", DBTAG), zap.String("nodeId", tgf.NodeId), zap.String("db", dbName), zap.Int32("count", count), zap.String("traceId", traceId))
}

func Service(module, name, version, userId string, consume int64, code int32) {
	logger.Debug("", zap.String("tag", SERVICETAG),
		zap.String("userId", userId),
		zap.String("module", module),
		zap.String("name", name),
		zap.String("version", version),
		zap.Int64("consume", consume),
		zap.Int32("code", code),
	)
}

// CheckLogTag 保留给老代码/外部调用兼容。新代码应当直接用 `*TagW` 系列。
func CheckLogTag(tag string) bool {
	return !ignoredTags[tag]
}

func initLogger() {
	var (
		/*自定义时间格式*/
		customTimeEncoder = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
		}
		/*自定义日志级别显示*/
		customLevelEncoder = func(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(level.CapitalString())
		}
		/*自定义代码路径、行号输出*/
		customCallerEncoder = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString("[" + caller.TrimmedPath() + "]")
		}
		logLevel = tgf.GetStrConfig[string](tgf.EnvironmentLoggerLevel)
		logPath  = tgf.GetStrConfig[string](tgf.EnvironmentLoggerPath)
	)

	zapLoggerEncoderConfig := zapcore.EncoderConfig{
		TimeKey:          "time",
		LevelKey:         "level",
		NameKey:          "logger",
		CallerKey:        "caller",
		MessageKey:       "message",
		StacktraceKey:    "stacktrace",
		EncodeCaller:     customCallerEncoder,
		EncodeTime:       customTimeEncoder,
		EncodeLevel:      customLevelEncoder,
		EncodeDuration:   zapcore.SecondsDurationEncoder,
		LineEnding:       "\n",
		ConsoleSeparator: " ",
	}

	//Dev环境,日志级别使用带颜色的标识
	if tgf.GetStrConfig[string](tgf.EnvironmentRuntimeModule) == tgf.RuntimeModuleDev {
		zapLoggerEncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	ignoredTags = make(map[string]bool)
	it := tgf.GetStrConfig[string](tgf.EnvironmentLoggerIgnoredTags)
	if it != "" {
		tags := strings.Split(it, ",")
		for _, tag := range tags {
			ignoredTags[tag] = true
		}
	}

	//在原有日志基础上增加一层
	level, _ := zapcore.ParseLevel(logLevel)
	//
	st := newCore(logPath, level, zapLoggerEncoderConfig, true)
	zapCoreGame := newCore(logPath, level, zapLoggerEncoderConfig, false)
	basePath := filepath.Dir(logPath)
	zapCoreService := newCore(fmt.Sprintf("%s%sservice%sservice.log", basePath, string(filepath.Separator), string(filepath.Separator)), level, zapLoggerEncoderConfig, false)
	zapCoreDB := newCore(fmt.Sprintf("%s%sdb%sdb.log", basePath, string(filepath.Separator), string(filepath.Separator)), level, zapLoggerEncoderConfig, false)
	// 创建一个映射，将标签映射到对应的Core
	taggedCores := map[string]zapcore.Core{
		DBTAG:      &TaggedCore{Core: zapCoreDB, Tag: DBTAG, Pass: true},
		GAMETAG:    &TaggedCore{Core: zapCoreGame, Tag: ""},
		SERVICETAG: &TaggedCore{Core: zapCoreService, Tag: SERVICETAG, Pass: true},
	}
	logger = zap.New(zapcore.NewTee(taggedCores[DBTAG], taggedCores[GAMETAG], taggedCores[SERVICETAG], st), zap.AddCaller(), zap.AddCallerSkip(1))
	slogger = logger.Sugar()
	defer logger.Sync()
	InfoTag("init", "日志初始化完成日志文件:%s 日志级别:%v", logPath, logLevel)
}

// 为每个日志类型（game, system, all）创建一个专门的Core实例
func newCore(logPath string, level zapcore.Level, zapLoggerEncoderConfig zapcore.EncoderConfig, stdout bool) zapcore.Core {
	//如果logPath文件夹不存在则创建
	if _, err := os.Stat(filepath.Dir(logPath)); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(logPath), os.ModePerm)
	}

	wys := make([]zapcore.WriteSyncer, 0, 2)
	if stdout {
		wys = append(wys, zapcore.AddSync(os.Stdout))
		syncWriter := zapcore.NewMultiWriteSyncer(wys...)
		return zapcore.NewCore(zapcore.NewConsoleEncoder(zapLoggerEncoderConfig), syncWriter, level)
	}
	wy := zapcore.AddSync(&lumberjack.Logger{
		Filename:   logPath,           // ⽇志⽂件路径
		MaxBackups: defaultMaxBackups, // 单位为MB,默认为512MB
		MaxSize:    defaultMaxSize,    // 单位为MB,默认为512MB
		MaxAge:     defaultMaxAge,     // 文件最多保存多少天
		LocalTime:  true,              // 采用本地时间
		Compress:   false,             // 是否压缩日志
	})
	wys = append(wys, wy)
	syncWriter := zapcore.NewMultiWriteSyncer(wys...)
	return zapcore.NewCore(zapcore.NewJSONEncoder(zapLoggerEncoderConfig), syncWriter, level)
}

type TaggedCore struct {
	Core        zapcore.Core
	Tag         string
	AllowedTags map[string]zapcore.Core
	Pass        bool
}

func (t *TaggedCore) Enabled(lvl zapcore.Level) bool {
	return t.Core.Enabled(lvl)
}

func (t *TaggedCore) With(fields []zapcore.Field) zapcore.Core {
	return &TaggedCore{
		Core:        t.Core.With(fields),
		Tag:         t.Tag,
		AllowedTags: t.AllowedTags,
	}
}

func (t *TaggedCore) Check(entry zapcore.Entry, checkedEntry *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if t.Pass || t.Core.Enabled(entry.Level) {
		return checkedEntry.AddCore(entry, t)
	}
	return nil
}

func (t *TaggedCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// 在这里可以根据标签过滤逻辑处理日志条目
	// 例如，你可以在fields中查找特定的标签字段，并根据这个标签决定是否调用t.Core.Write
	if t.Tag == "" {
		return t.Core.Write(entry, fields)
	}

	for _, field := range fields {
		if field.Key == "tag" && field.String == t.Tag {
			return t.Core.Write(entry, fields)
		}
	}
	return nil
}

func (t *TaggedCore) Sync() error {
	return t.Core.Sync()
}
