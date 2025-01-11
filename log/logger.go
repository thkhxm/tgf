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

func Info(msg string, params ...interface{}) {
	logger.Info(fmt.Sprintf(msg, params...))
}

func InfoTag(tag string, msg string, params ...interface{}) {
	if CheckLogTag(tag) {
		logger.Info(fmt.Sprintf(msg, params...), zap.String("tag", tag))
	}
}

func SLogger() *zap.SugaredLogger {
	return slogger
}

func Game(userId, tag, msg string, params ...interface{}) {
	//if CheckLogTag(GAMETAG) {
	logger.Info(fmt.Sprintf(msg, params...), zap.String("tag", tag), zap.String("userId", userId))
	//}
}

func DB(traceId, dbName, script string, count int32) {
	//if CheckLogTag(DBTAG) {
	logger.Debug(script, zap.String("tag", DBTAG), zap.String("nodeId", tgf.NodeId), zap.String("db", dbName), zap.Int32("count", count), zap.String("traceId", traceId))
	//}
}

func Service(module, name, version, userId string, consume int64, code int32) {
	//if CheckLogTag(SERVICETAG) {
	logger.Debug("", zap.String("tag", SERVICETAG),
		zap.String("userId", userId),
		zap.String("module", module),
		zap.String("name", name),
		zap.String("version", version),
		zap.Int64("consume", consume),
		zap.Int32("code", code),
	)
	//}
}

func Debug(msg string, params ...interface{}) {
	logger.Debug(fmt.Sprintf(msg, params...))
}

func DebugTag(tag string, msg string, params ...interface{}) {
	if CheckLogTag(tag) {
		logger.Debug(fmt.Sprintf(msg, params...), zap.String("tag", tag))
	}
}

func Error(msg string, params ...interface{}) {
	logger.Error(fmt.Sprintf(msg, params...))
}

func ErrorTag(tag string, msg string, params ...interface{}) {
	if CheckLogTag(tag) {
		logger.Error(fmt.Sprintf(msg, params...), zap.String("tag", tag))
	}
}

func Warn(msg string, params ...interface{}) {
	logger.Warn(fmt.Sprintf(msg, params...))
}

func WarnTag(tag string, msg string, params ...interface{}) {
	if CheckLogTag(tag) {
		logger.Warn(fmt.Sprintf(msg, params...), zap.String("tag", tag))
	}
}

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

	//syncWriter = zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout))

	//syncWriter := zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout), zapcore.AddSync(&lumberjack.Logger{
	//	Filename:  logPath,        // ⽇志⽂件路径
	//	MaxSize:   defaultMaxSize, // 单位为MB,默认为512MB
	//	MaxAge:    defaultMaxAge,  // 文件最多保存多少天
	//	LocalTime: true,           // 采用本地时间
	//	Compress:  false,          // 是否压缩日志
	//}))

	//syncWriter = &zapcore.BufferedWriteSyncer{
	//	WS: zapcore.AddSync(&lumberjack.Logger{
	//		Filename:  "logs/app/app.log", // ⽇志⽂件路径
	//		MaxSize:   100,                                                                                                        // 单位为MB,默认为512MB
	//		MaxAge:    5,                                                                                                          // 文件最多保存多少天
	//		LocalTime: true,                                                                                                       // 采用本地时间
	//		Compress:  false,                                                                                                      // 是否压缩日志
	//	}),
	//	Size: 4096,
	//}
	//在原有日志基础上增加一层
	level, _ := zapcore.ParseLevel(logLevel)
	//
	st := newCore(logPath, level, zapLoggerEncoderConfig, true)
	zapCoreGame := newCore(logPath, level, zapLoggerEncoderConfig, false)
	basePath := filepath.Dir(logPath)
	zapCoreService := newCore(fmt.Sprintf("%s%sservice%sservice.log", basePath, string(filepath.Separator), string(filepath.Separator)), level, zapLoggerEncoderConfig, false)
	zapCoreDB := newCore(fmt.Sprintf("%s%sdb%sdb.log", basePath, string(filepath.Separator), string(filepath.Separator)), level, zapLoggerEncoderConfig, false)
	//zapCore := zapcore.NewCore(zapcore.NewConsoleEncoder(zapLoggerEncoderConfig), syncWriter, level)
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
