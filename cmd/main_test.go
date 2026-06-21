package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"go.uber.org/zap"

	"github.com/Zozman/stream-webpage-container/utils"
)

func resetGlobalStreamState() {
	globalStreamState.mu.Lock()
	defer globalStreamState.mu.Unlock()
	globalStreamState.isRunning = false
	globalStreamState.cancelFunc = nil
	globalStreamState.chromeCancels = nil
	globalStreamState.xvfbCmds = nil
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
			Outputs: []StreamOutput{
				{Width: 1280, Height: 720, Framerate: 30, VideoBitrate: "3000k", Name: "primary", Display: 100},
			},
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
		globalStreamState.setStreamRunning(cancel, []context.CancelFunc{chromeCancel}, nil, mockCmd)

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

		_, cancel := context.WithCancel(context.Background())
		_, chromeCancel := context.WithCancel(context.Background())
		mockCmd := &exec.Cmd{}
		globalStreamState.setStreamRunning(cancel, []context.CancelFunc{chromeCancel}, nil, mockCmd)

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
	t.Run("Default Configuration (no STREAM_OUTPUTS)", func(t *testing.T) {
		t.Setenv("WEBPAGE_URL", "")
		t.Setenv("RTMP_URL", "")
		t.Setenv("RESOLUTION", "")
		t.Setenv("FRAMERATE", "")
		t.Setenv("STREAM_OUTPUTS", "")

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
		if len(config.Outputs) != 1 {
			t.Fatalf("Expected 1 output, got %d", len(config.Outputs))
		}
		if config.Outputs[0].Width != 1280 || config.Outputs[0].Height != 720 {
			t.Errorf("Expected 720p dimensions, got %dx%d", config.Outputs[0].Width, config.Outputs[0].Height)
		}
		if config.Outputs[0].Framerate != 30 {
			t.Errorf("Expected framerate 30, got %d", config.Outputs[0].Framerate)
		}
	})

	t.Run("Custom Single Output", func(t *testing.T) {
		t.Setenv("WEBPAGE_URL", "https://custom.example.com")
		t.Setenv("RTMP_URL", "rtmp://custom.example.com/live/test")
		t.Setenv("RESOLUTION", "1080p")
		t.Setenv("FRAMERATE", "60")
		t.Setenv("STREAM_OUTPUTS", "")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.WebpageURL != "https://custom.example.com" {
			t.Errorf("Expected custom webpage URL, got %q", config.WebpageURL)
		}
		if len(config.Outputs) != 1 {
			t.Fatalf("Expected 1 output, got %d", len(config.Outputs))
		}
		if config.Outputs[0].Width != 1920 || config.Outputs[0].Height != 1080 {
			t.Errorf("Expected 1080p dimensions, got %dx%d", config.Outputs[0].Width, config.Outputs[0].Height)
		}
		if config.Outputs[0].Framerate != 60 {
			t.Errorf("Expected framerate 60, got %d", config.Outputs[0].Framerate)
		}
	})

	t.Run("STREAM_OUTPUTS Multi-track With Vertical", func(t *testing.T) {
		outputs := `[
			{"width":1920,"height":1080,"framerate":60,"videoBitrate":"6000k","name":"desktop"},
			{"width":1080,"height":1920,"framerate":30,"videoBitrate":"4500k","name":"vertical"},
			{"resolution":"360p","framerate":30}
		]`
		t.Setenv("STREAM_OUTPUTS", outputs)
		t.Setenv("RTMP_URL", "rtmp://test/live/key")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if len(config.Outputs) != 3 {
			t.Fatalf("Expected 3 outputs, got %d", len(config.Outputs))
		}

		// Track 0: desktop 1920x1080
		if config.Outputs[0].Width != 1920 || config.Outputs[0].Height != 1080 {
			t.Errorf("Output 0: expected 1920x1080, got %dx%d", config.Outputs[0].Width, config.Outputs[0].Height)
		}
		if config.Outputs[0].Framerate != 60 {
			t.Errorf("Output 0: expected framerate 60, got %d", config.Outputs[0].Framerate)
		}
		if config.Outputs[0].VideoBitrate != "6000k" {
			t.Errorf("Output 0: expected bitrate 6000k, got %q", config.Outputs[0].VideoBitrate)
		}

		// Track 1: vertical 1080x1920
		if config.Outputs[1].Width != 1080 || config.Outputs[1].Height != 1920 {
			t.Errorf("Output 1: expected 1080x1920, got %dx%d", config.Outputs[1].Width, config.Outputs[1].Height)
		}
		if config.Outputs[1].Framerate != 30 {
			t.Errorf("Output 1: expected framerate 30, got %d", config.Outputs[1].Framerate)
		}
		if config.Outputs[1].Name != "vertical" {
			t.Errorf("Output 1: expected name 'vertical', got %q", config.Outputs[1].Name)
		}

		// Track 2: 360p resolved from preset
		if config.Outputs[2].Width != 640 || config.Outputs[2].Height != 360 {
			t.Errorf("Output 2: expected 640x360, got %dx%d", config.Outputs[2].Width, config.Outputs[2].Height)
		}
		if config.Outputs[2].VideoBitrate != "1000k" {
			t.Errorf("Output 2: expected derived bitrate 1000k, got %q", config.Outputs[2].VideoBitrate)
		}

		// Display numbers assigned correctly
		for i, out := range config.Outputs {
			expected := BaseDisplayNumber + i
			if out.Display != expected {
				t.Errorf("Output %d: expected display %d, got %d", i, expected, out.Display)
			}
		}
	})

	t.Run("STREAM_OUTPUTS With Resolution Preset", func(t *testing.T) {
		outputs := `[{"resolution":"2k","framerate":60}]`
		t.Setenv("STREAM_OUTPUTS", outputs)

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.Outputs[0].Width != 2560 || config.Outputs[0].Height != 1440 {
			t.Errorf("Expected 2k dimensions 2560x1440, got %dx%d", config.Outputs[0].Width, config.Outputs[0].Height)
		}
		if config.Outputs[0].VideoBitrate != "8500k" {
			t.Errorf("Expected derived bitrate 8500k for 2k@60, got %q", config.Outputs[0].VideoBitrate)
		}
	})

	t.Run("Invalid STREAM_OUTPUTS JSON", func(t *testing.T) {
		t.Setenv("STREAM_OUTPUTS", "not valid json")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		_, err := loadConfig(ctx)
		if err == nil {
			t.Fatal("Expected error for invalid JSON, got nil")
		}
	})

	t.Run("STREAM_OUTPUTS Empty Array", func(t *testing.T) {
		t.Setenv("STREAM_OUTPUTS", "[]")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		_, err := loadConfig(ctx)
		if err == nil {
			t.Fatal("Expected error for empty array, got nil")
		}
	})

	t.Run("Invalid Resolution In STREAM_OUTPUTS", func(t *testing.T) {
		t.Setenv("STREAM_OUTPUTS", `[{"resolution":"4k"}]`)

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		_, err := loadConfig(ctx)
		if err == nil {
			t.Fatal("Expected error for unsupported resolution, got nil")
		}
	})

	t.Run("Missing Width/Height Without Resolution", func(t *testing.T) {
		t.Setenv("STREAM_OUTPUTS", `[{"framerate":30}]`)

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		_, err := loadConfig(ctx)
		if err == nil {
			t.Fatal("Expected error for missing dimensions, got nil")
		}
	})

	t.Run("Invalid Resolution Falls Back To 720p (single output mode)", func(t *testing.T) {
		t.Setenv("RESOLUTION", "invalid_resolution")
		t.Setenv("STREAM_OUTPUTS", "")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.Outputs[0].Width != 1280 || config.Outputs[0].Height != 720 {
			t.Errorf("Expected 720p fallback, got %dx%d", config.Outputs[0].Width, config.Outputs[0].Height)
		}
	})

	t.Run("Invalid Framerate Falls Back To 30 (single output mode)", func(t *testing.T) {
		t.Setenv("FRAMERATE", "invalid_framerate")
		t.Setenv("STREAM_OUTPUTS", "")

		logger, _ := zap.NewDevelopment()
		ctx := utils.SaveLoggerToContext(context.Background(), logger)

		config, err := loadConfig(ctx)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config.Outputs[0].Framerate != 30 {
			t.Errorf("Expected framerate fallback 30, got %d", config.Outputs[0].Framerate)
		}
	})
}

func TestResolveResolution(t *testing.T) {
	tests := []struct {
		input          string
		expectedWidth  int
		expectedHeight int
		expectError    bool
	}{
		{"360p", 640, 360, false},
		{"720p", 1280, 720, false},
		{"1080p", 1920, 1080, false},
		{"2k", 2560, 1440, false},
		{"invalid", 0, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			w, h, err := resolveResolution(tc.input)
			if tc.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("Expected no error, got %v", err)
				}
				if w != tc.expectedWidth || h != tc.expectedHeight {
					t.Errorf("Expected %dx%d, got %dx%d", tc.expectedWidth, tc.expectedHeight, w, h)
				}
			}
		})
	}
}

func TestDeriveBitrate(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		height   int
		fps      int
		expected string
	}{
		{"360p@30", 640, 360, 30, "1000k"},
		{"360p@60", 640, 360, 60, "1000k"},
		{"720p@30", 1280, 720, 30, "3000k"},
		{"720p@60", 1280, 720, 60, "4000k"},
		{"1080p@30", 1920, 1080, 30, "4500k"},
		{"1080p@60", 1920, 1080, 60, "6000k"},
		{"vertical 1080x1920@30", 1080, 1920, 30, "4500k"},
		{"vertical 1080x1920@60", 1080, 1920, 60, "6000k"},
		{"2k@30", 2560, 1440, 30, "6000k"},
		{"2k@60", 2560, 1440, 60, "8500k"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := deriveBitrate(tc.width, tc.height, tc.fps)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
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
