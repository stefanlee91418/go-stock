package logger

import (
	"go-stock/backend/apppath"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"path/filepath"
)

// ModelSugaredLogger 专用模型调用日志，写入 logs/model.log，
// 与 info.log 分离，避免被行情 DEBUG 刷屏淹没。
var ModelSugaredLogger *zap.SugaredLogger

func init() {
	InitModelLogger()
}

// InitModelLogger 初始化模型调用专用日志（可重复调用，幂等替换）。
func InitModelLogger() {
	encoder := getEncoder()
	ws := zapcore.AddSync(&lumberjack.Logger{
		Filename:   filepath.Join(apppath.LogsDir(), "model.log"),
		MaxSize:    20, // megabytes
		MaxBackups: 50,
		MaxAge:     28,
		Compress:   false,
	})
	// 仅写文件，不重复打控制台（主 Logger 已有 stdout）
	core := zapcore.NewCore(encoder, ws, zap.DebugLevel)
	ModelSugaredLogger = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).Sugar()
}

// ModelInfof 记录模型调用信息到 logs/model.log。
func ModelInfof(template string, args ...interface{}) {
	if ModelSugaredLogger != nil {
		ModelSugaredLogger.Infof(template, args...)
	}
}

// ModelWarnf 记录模型调用告警到 logs/model.log。
func ModelWarnf(template string, args ...interface{}) {
	if ModelSugaredLogger != nil {
		ModelSugaredLogger.Warnf(template, args...)
	}
}

// ModelErrorf 记录模型调用错误到 logs/model.log。
func ModelErrorf(template string, args ...interface{}) {
	if ModelSugaredLogger != nil {
		ModelSugaredLogger.Errorf(template, args...)
	}
}
