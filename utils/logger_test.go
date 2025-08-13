package utils

import (
	"context"
	"sync"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Reset the logger singleton for testing
func resetLogger() {
	logger = nil
	loggerOnce = sync.Once{}
}

func TestGetLogger(t *testing.T) {
	t.Cleanup(func() {
		resetLogger()
	})

	t.Run("Logger Is Returned", func(t *testing.T) {
		resetLogger()

		logger1 := GetLogger()

		if logger1 == nil {
			t.Fatal("Expected logger to be non-nil")
		}
	})
}

func TestInitializeLogger(t *testing.T) {
	t.Run("Default Logger", func(t *testing.T) {
		logger, err := initializeLogger()

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if logger == nil {
			t.Fatal("Expected logger to be non-nil")
		}
	})

	t.Run("Debug Log Level", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "debug")
		t.Setenv("LOG_FORMAT", "json")

		logger, err := initializeLogger()

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if logger == nil {
			t.Fatal("Expected logger to be non-nil")
		}
		if !logger.Core().Enabled(zapcore.DebugLevel) {
			t.Error("Expected logger to be enabled for debug level")
		}
	})

	t.Run("Invalid Log Level Defaults To Info", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "invalid_level")
		t.Setenv("LOG_FORMAT", "json")

		logger, err := initializeLogger()

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if !logger.Core().Enabled(zapcore.InfoLevel) {
			t.Error("Expected logger to default to info level for invalid log level")
		}
		if logger.Core().Enabled(zapcore.DebugLevel) {
			t.Error("Expected logger to be disabled for debug level when defaulting to info")
		}
	})

	t.Run("Console Log Format", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "info")
		t.Setenv("LOG_FORMAT", "console")

		logger, err := initializeLogger()

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if logger == nil {
			t.Fatal("Expected logger to be non-nil")
		}
	})

	t.Run("JSON Log Format", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "info")
		t.Setenv("LOG_FORMAT", "json")

		logger, err := initializeLogger()

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if logger == nil {
			t.Fatal("Expected logger to be non-nil")
		}
	})

	t.Run("Invalid Log Format Defaults To JSON", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "info")
		t.Setenv("LOG_FORMAT", "invalid_format")

		logger, err := initializeLogger()

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if logger == nil {
			t.Fatal("Expected logger to be non-nil when using invalid format")
		}
	})
}

func TestGetLoggerFromContext(t *testing.T) {
	t.Cleanup(func() {
		resetLogger()
	})

	t.Run("Logger Found In Context", func(t *testing.T) {
		// Create a test logger
		testLogger, err := zap.NewDevelopment()
		if err != nil {
			t.Fatalf("Failed to create test logger: %v", err)
		}

		// Save logger to context
		ctx := context.Background()
		ctx = SaveLoggerToContext(ctx, testLogger)

		// Retrieve logger from context
		retrievedLogger := GetLoggerFromContext(ctx)

		if retrievedLogger != testLogger {
			t.Error("Expected to retrieve the same logger from context")
		}
	})

	t.Run("Logger Not Found In Context Falls Back To Global", func(t *testing.T) {
		resetLogger()
		ctx := context.Background()

		// Initialize global logger first
		globalLogger := GetLogger()

		// Try to get logger from empty context
		retrievedLogger := GetLoggerFromContext(ctx)

		if retrievedLogger != globalLogger {
			t.Error("Expected to fallback to global logger when not found in context")
		}
	})
}

func TestSaveLoggerToContext(t *testing.T) {
	t.Run("Logger Saved To Context Successfully", func(t *testing.T) {
		// Create a test logger
		testLogger, err := zap.NewDevelopment()
		if err != nil {
			t.Fatalf("Failed to create test logger: %v", err)
		}

		ctx := context.Background()
		newCtx := SaveLoggerToContext(ctx, testLogger)

		// Verify the logger was saved by retrieving it
		retrievedLogger := GetLoggerFromContext(newCtx)

		if retrievedLogger != testLogger {
			t.Error("Expected to retrieve the same logger that was saved to context")
		}
	})

	t.Run("Context Is Not Modified In Place", func(t *testing.T) {
		// Create a test logger
		testLogger, err := zap.NewDevelopment()
		if err != nil {
			t.Fatalf("Failed to create test logger: %v", err)
		}

		originalCtx := context.Background()
		newCtx := SaveLoggerToContext(originalCtx, testLogger)

		// Verify original context doesn't have the logger
		if originalCtx.Value(loggerKey) != nil {
			t.Error("Expected original context to remain unmodified")
		}

		// Verify new context has the logger
		if newCtx.Value(loggerKey) != testLogger {
			t.Error("Expected new context to contain the logger")
		}
	})
}
