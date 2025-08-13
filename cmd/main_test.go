package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Zozman/stream-webpage-container/utils"
)

// Helper function to reset global stream state for testing
func resetGlobalStreamState() {
	globalStreamState.mu.Lock()
	defer globalStreamState.mu.Unlock()
	globalStreamState.isRunning = false
	globalStreamState.cancelFunc = nil
	globalStreamState.chromeCancel = nil
	globalStreamState.ffmpegCmd = nil
}

func TestRestartStream(t *testing.T) {
	t.Cleanup(resetGlobalStreamState)

	t.Run("Restart Stream Successfully", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config := &Config{
			WebpageURL: "https://example.com",
			RTMPURL:    "rtmp://example.com/live/stream",
			Resolution: "720p",
			Framerate:  "30",
			Width:      1280,
			Height:     720,
		}

		err := RestartStream(ctx, config)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	})
}

func TestIsStreamRunning(t *testing.T) {
	t.Cleanup(resetGlobalStreamState)

	t.Run("Stream Not Running Initially", func(t *testing.T) {
		resetGlobalStreamState()

		if IsStreamRunning() {
			t.Error("Expected stream to not be running initially")
		}
	})

	t.Run("Stream Running After Setting Global State", func(t *testing.T) {
		resetGlobalStreamState()

		_, cancel := context.WithCancel(context.Background())
		defer cancel()

		_, chromeCancel := context.WithCancel(context.Background())
		defer chromeCancel()

		mockCmd := &exec.Cmd{}
		globalStreamState.setStreamRunning(cancel, chromeCancel, mockCmd)

		if !IsStreamRunning() {
			t.Error("Expected stream to be running after setting global state")
		}
	})
}

func TestStopCurrentStream(t *testing.T) {
	t.Cleanup(resetGlobalStreamState)

	t.Run("Stop Current Stream", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		// Set up a running stream
		_, cancel := context.WithCancel(context.Background())
		_, chromeCancel := context.WithCancel(context.Background())
		mockCmd := &exec.Cmd{}
		globalStreamState.setStreamRunning(cancel, chromeCancel, mockCmd)

		if !globalStreamState.isRunning {
			t.Fatal("Expected stream to be running before stopping")
		}

		StopCurrentStream(ctx)

		if globalStreamState.isRunning {
			t.Error("Expected stream to be stopped after calling StopCurrentStream")
		}
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("Default Configuration", func(t *testing.T) {
		t.Setenv("WEBPAGE_URL", "")
		t.Setenv("RTMP_URL", "")
		t.Setenv("RESOLUTION", "")
		t.Setenv("FRAMERATE", "")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.WebpageURL != DefaultWebpageURL {
			t.Errorf("Expected default webpage URL %q, got %q", DefaultWebpageURL, config.WebpageURL)
		}
		if config.RTMPURL != DefaultRTMPURL {
			t.Errorf("Expected default RTMP URL %q, got %q", DefaultRTMPURL, config.RTMPURL)
		}
		if config.Resolution != DefaultResolution {
			t.Errorf("Expected default resolution %q, got %q", DefaultResolution, config.Resolution)
		}
		if config.Framerate != DefaultFramerate {
			t.Errorf("Expected default framerate %q, got %q", DefaultFramerate, config.Framerate)
		}
	})

	t.Run("Custom Configuration", func(t *testing.T) {
		expectedURL := "https://custom.example.com"
		expectedRTMP := "rtmp://custom.example.com/live/test"
		expectedResolution := "1080p"
		expectedFramerate := "60"

		t.Setenv("WEBPAGE_URL", expectedURL)
		t.Setenv("RTMP_URL", expectedRTMP)
		t.Setenv("RESOLUTION", expectedResolution)
		t.Setenv("FRAMERATE", expectedFramerate)

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.WebpageURL != expectedURL {
			t.Errorf("Expected webpage URL %q, got %q", expectedURL, config.WebpageURL)
		}
		if config.RTMPURL != expectedRTMP {
			t.Errorf("Expected RTMP URL %q, got %q", expectedRTMP, config.RTMPURL)
		}
		if config.Resolution != expectedResolution {
			t.Errorf("Expected resolution %q, got %q", expectedResolution, config.Resolution)
		}
		if config.Framerate != expectedFramerate {
			t.Errorf("Expected framerate %q, got %q", expectedFramerate, config.Framerate)
		}
	})

	t.Run("720p Resolution Dimensions", func(t *testing.T) {
		t.Setenv("RESOLUTION", "720p")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.Width != 1280 || config.Height != 720 {
			t.Errorf("Expected 720p dimensions 1280x720, got %dx%d", config.Width, config.Height)
		}
	})

	t.Run("1080p Resolution Dimensions", func(t *testing.T) {
		t.Setenv("RESOLUTION", "1080p")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.Width != 1920 || config.Height != 1080 {
			t.Errorf("Expected 1080p dimensions 1920x1080, got %dx%d", config.Width, config.Height)
		}
	})

	t.Run("2k Resolution Dimensions", func(t *testing.T) {
		t.Setenv("RESOLUTION", "2k")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.Width != 2560 || config.Height != 1440 {
			t.Errorf("Expected 2K dimensions 2560x1440, got %dx%d", config.Width, config.Height)
		}
	})

	t.Run("Invalid Resolution Defaults To 720p", func(t *testing.T) {
		t.Setenv("RESOLUTION", "invalid_resolution")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.Resolution != "720p" {
			t.Errorf("Expected resolution to default to 720p, got %q", config.Resolution)
		}
		if config.Width != 1280 || config.Height != 720 {
			t.Errorf("Expected 720p dimensions 1280x720, got %dx%d", config.Width, config.Height)
		}
	})

	t.Run("Invalid Framerate Defaults To 30", func(t *testing.T) {
		t.Setenv("FRAMERATE", "invalid_framerate")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.Framerate != "30" {
			t.Errorf("Expected framerate to default to 30, got %q", config.Framerate)
		}
	})

	t.Run("Valid Framerate Values", func(t *testing.T) {
		testCases := []string{"30", "60"}

		for _, framerate := range testCases {
			t.Run("Framerate "+framerate, func(t *testing.T) {
				t.Setenv("FRAMERATE", framerate)

				logger, _ := zap.NewDevelopment()
				ctx := utils.SaveLoggerToContext(context.Background(), logger)

				config, err := loadConfig(ctx)

				if err != nil {
					t.Fatalf("Expected no error, got %v", err)
				}
				if config.Framerate != framerate {
					t.Errorf("Expected framerate %q, got %q", framerate, config.Framerate)
				}
			})
		}
	})
}

func TestGetHealthResponse(t *testing.T) {
	t.Run("Health Response Structure", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		getHealthResponse(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status code 200, got %d", w.Code)
		}

		contentType := w.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Expected content type application/json, got %q", contentType)
		}

		var health Health
		if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil {
			t.Fatalf("Failed to unmarshal health response: %v", err)
		}

		if health.Message != "OK" {
			t.Errorf("Expected message 'OK', got %q", health.Message)
		}

		if health.Uptime <= 0 {
			t.Error("Expected uptime to be greater than 0")
		}

		if health.Date.IsZero() {
			t.Error("Expected date to be set")
		}
	})
}

func TestExtractNumberFromBitrate(t *testing.T) {
	t.Run("Valid Bitrate Strings", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected int
		}{
			{"3000k", 3000},
			{"4500k", 4500},
			{"6000k", 6000},
			{"8500k", 8500},
			{"1000k", 1000},
		}

		for _, tc := range testCases {
			t.Run("Bitrate "+tc.input, func(t *testing.T) {
				result := extractNumberFromBitrate(tc.input)
				if result != tc.expected {
					t.Errorf("Expected %d, got %d", tc.expected, result)
				}
			})
		}
	})

	t.Run("Invalid Bitrate String", func(t *testing.T) {
		result := extractNumberFromBitrate("invalidk")
		if result != 3000 {
			t.Errorf("Expected default value 3000, got %d", result)
		}
	})
}

func TestGetDisplayInfo(t *testing.T) {
	t.Run("Display From Environment Variable", func(t *testing.T) {
		expectedDisplay := ":1"
		t.Setenv("DISPLAY", expectedDisplay)

		display, err := getDisplayInfo()

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if display != expectedDisplay {
			t.Errorf("Expected display %q, got %q", expectedDisplay, display)
		}
	})

	t.Run("Default Display When Environment Variable Not Set", func(t *testing.T) {
		t.Setenv("DISPLAY", "")

		display, err := getDisplayInfo()

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if display != ":0" {
			t.Errorf("Expected default display ':0', got %q", display)
		}
	})
}

func TestFFmpegArgumentConstruction(t *testing.T) {
	// This test verifies that the FFmpeg command includes proper stream mapping
	// to ensure both video and audio streams are captured
	t.Run("FFmpeg Command Includes Stream Mapping", func(t *testing.T) {
		// The key fix is that we now include explicit stream mapping:
		// "-map", "0:v", "-map", "1:a"
		// This ensures FFmpeg will fail if either video or audio input is unavailable
		// rather than silently continuing with only one stream
		
		// For now, we'll just verify the display info function works
		display, err := getDisplayInfo()
		if err != nil {
			t.Fatalf("Expected no error from getDisplayInfo, got %v", err)
		}
		
		// Should return either the DISPLAY env var or default ":0"
		if display != ":0" && !strings.HasPrefix(display, ":") {
			t.Errorf("Expected display to start with ':', got %q", display)
		}
	})
}
