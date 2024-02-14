package logging

import (
	"fmt"
	"io"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	prettyconsole "github.com/thessem/zap-prettyconsole"
)

type LogLevel = zapcore.Level

const (
	// DebugLevel logs are typically voluminous, and are usually disabled in
	// production.
	DebugLevel = zapcore.DebugLevel
	// InfoLevel is the default logging priority.
	InfoLevel = zapcore.InfoLevel
	// WarnLevel logs are more important than Info, but don't need individual
	// human review.
	WarnLevel = zapcore.WarnLevel
	// ErrorLevel logs are high-priority. If an application is running smoothly,
	// it shouldn't generate any error-level logs.
	ErrorLevel = zapcore.ErrorLevel
	// DPanicLevel logs are particularly important errors. In development the
	// logger panics after writing the message.
	DPanicLevel = zapcore.DPanicLevel
	// PanicLevel logs a message, then panics.
	PanicLevel = zapcore.PanicLevel
	// FatalLevel logs a message, then calls os.Exit(1).
	FatalLevel = zapcore.FatalLevel
)

func zapLogLevel(level string) zapcore.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "dpanic":
		return zapcore.DPanicLevel
	case "panic":
		return zapcore.PanicLevel
	case "fatal":
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

func initDefLogger(writer io.Writer, level zapcore.Level, env string, extraOpts ...zap.Option) {
	var core zapcore.Core
	defLevelEnabler = zap.NewAtomicLevelAt(level)

	opts := make([]zap.Option, 0, len(extraOpts))
	switch env {
	case "production":
		cfg := zap.NewProductionEncoderConfig()
		cfg.EncodeTime = zapcore.ISO8601TimeEncoder
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg),
			zapcore.Lock(zapcore.AddSync(writer)),
			defLevelEnabler,
		)
	case "development":
		cfg := prettyconsole.NewEncoderConfig()
		cfg.CallerKey = "C"
		cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		cfg.EncodeTime = prettyconsole.DefaultTimeEncoder("15:04:05.000")
		opts = append(opts, zap.WithCaller(true), zap.AddCallerSkip(1), zap.AddStacktrace(zapcore.ErrorLevel))
		core = zapcore.NewCore(
			prettyconsole.NewEncoder(cfg),
			zapcore.Lock(zapcore.AddSync(writer)),
			defLevelEnabler,
		)
	case "test":
		cfg := zapcore.EncoderConfig{
			MessageKey:     "msg",
			LevelKey:       "level",
			NameKey:        "logger",
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
		}
		opts = append(opts, zap.WithCaller(true))
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(cfg),
			zapcore.Lock(zapcore.AddSync(writer)),
			defLevelEnabler,
		)
	}
	opts = append(opts, extraOpts...)

	defLogger = zap.New(core, opts...).Sugar()
}

// wrappers for default logger
func Level() LogLevel {
	if defLogger != nil {
		return defLogger.Level()
	}
	return zapcore.InvalidLevel
}

func Debug(args ...any) {
	if defLogger != nil {
		defLogger.Debug(args...)
	}
}

func Info(args ...any) {
	if defLogger != nil {
		defLogger.Info(args...)
	}
}

func StdInfo(format string, v ...any) {
	Info(fmt.Sprintf(format, v...))
}

func Warn(args ...any) {
	if defLogger != nil {
		defLogger.Warn(args...)
	}
}

func Error(args ...any) {
	if defLogger != nil {
		defLogger.Error(args...)
	}
}

func DPanic(args ...any) {
	if defLogger != nil {
		defLogger.DPanic(args...)
	}
}

func Panic(args ...any) {
	if defLogger != nil {
		defLogger.Panic(args...)
	}
}

func Fatal(args ...any) {
	if defLogger != nil {
		defLogger.Fatal(args...)
	}
}

func Debugf(template string, args ...any) {
	if defLogger != nil {
		defLogger.Debugf(template, args...)
	}
}

func Infof(template string, args ...any) {
	if defLogger != nil {
		defLogger.Infof(template, args...)
	}
}

func Warnf(template string, args ...any) {
	if defLogger != nil {
		defLogger.Warnf(template, args...)
	}
}

func Errorf(template string, args ...any) {
	if defLogger != nil {
		defLogger.Errorf(template, args...)
	}
}

func DPanicf(template string, args ...any) {
	if defLogger != nil {
		defLogger.DPanicf(template, args...)
	}
}

func Panicf(template string, args ...any) {
	if defLogger != nil {
		defLogger.Panicf(template, args...)
	}
}

func Fatalf(template string, args ...any) {
	if defLogger != nil {
		defLogger.Fatalf(template, args...)
	}
}

func Debugw(msg string, keysAndValues ...any) {
	if defLogger != nil {
		defLogger.Debugw(msg, keysAndValues...)
	}
}

func Infow(msg string, keysAndValues ...any) {
	if defLogger != nil {
		defLogger.Infow(msg, keysAndValues...)
	}
}

func Warnw(msg string, keysAndValues ...any) {
	if defLogger != nil {
		defLogger.Warnw(msg, keysAndValues...)
	}
}

func Errorw(msg string, keysAndValues ...any) {
	if defLogger != nil {
		defLogger.Errorw(msg, keysAndValues...)
	}
}

func DPanicw(msg string, keysAndValues ...any) {
	if defLogger != nil {
		defLogger.DPanicw(msg, keysAndValues...)
	}
}

func Panicw(msg string, keysAndValues ...any) {
	if defLogger != nil {
		defLogger.Panicw(msg, keysAndValues...)
	}
}

func Fatalw(msg string, keysAndValues ...any) {
	if defLogger != nil {
		defLogger.Fatalw(msg, keysAndValues...)
	}
}
