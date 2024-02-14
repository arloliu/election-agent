package logging

import (
	"os"

	"election-agent/internal/config"

	"go.uber.org/zap"
)

var (
	defLogger       *zap.SugaredLogger
	defLevelEnabler zap.AtomicLevel
)

func Init() *zap.SugaredLogger {
	logLevel := zapLogLevel(config.GetDefault().LogLevel)
	initDefLogger(os.Stderr, logLevel, config.Default.Env)

	return defLogger
}

func Default() *zap.SugaredLogger {
	return defLogger
}

func SetLevel(level LogLevel) {
	defLevelEnabler.SetLevel(level)
}
