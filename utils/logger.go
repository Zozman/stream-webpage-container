package utils

import (
	"context"
	"strings"
	"sync"

	"go.uber.org/zap"
)

const (
	defaultLogLevel  = "info"
	defaultLogFormat = "json"
	loggerKey        = "logger"
)

var (
	logger     *zap.Logger
	loggerOnce sync.Once
)

func GetLogger() *zap.Logger {
	loggerOnce.Do(func() {
		var err error
		logger, err = initializeLogger()
		if err != nil {
			panic("Failed to create logger: " + err.Error())
		}
		defer logger.Sync()
	})
	return logger
}

func initializeLogger() (*zap.Logger, error) {
	// Get basic log configuration from environment
	logLevel := GetEnvOrDefault("LOG_LEVEL", defaultLogLevel)
	logFormat := GetEnvOrDefault("LOG_FORMAT", defaultLogFormat)

	// Parse log level
	var level zap.AtomicLevel
	switch strings.ToLower(logLevel) {
	case "debug":
		level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn", "warning":
		level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	case "dpanic":
		level = zap.NewAtomicLevelAt(zap.DPanicLevel)
	case "panic":
		level = zap.NewAtomicLevelAt(zap.PanicLevel)
	case "fatal":
		level = zap.NewAtomicLevelAt(zap.FatalLevel)
	default:
		level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	// Configure logger based on format
	var config zap.Config
	switch strings.ToLower(logFormat) {
	case "console":
		config = zap.NewDevelopmentConfig()
		config.Level = level
	case "json":
		config = zap.NewProductionConfig()
		config.Level = level
	default:
		config = zap.NewProductionConfig()
		config.Level = level
	}

	return config.Build()
}

// Helper function to get logger from context
func GetLoggerFromContext(ctx context.Context) *zap.Logger {
	if logger, ok := ctx.Value(loggerKey).(*zap.Logger); ok {
		return logger
	}
	// Fallback to the default logger if not found in context
	return logger
}

// Helper function to save logger to context
func SaveLoggerToContext(ctx context.Context, logger *zap.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}
