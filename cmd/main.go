package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/nicklaw5/helix/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapio"

	"github.com/Zozman/stream-webpage-container/twitch"
	"github.com/Zozman/stream-webpage-container/utils"
)

const (
	// Default resolution for streams sent by the application
	DefaultResolution = "720p"
	// Default RTMP URL to stream to
	DefaultRTMPURL = "rtmp://localhost:1935/live/stream"
	// Default webpage to capture
	DefaultWebpageURL = "https://google.com"
	// Default framerate for the stream
	DefaultFramerate = "30"
	// Default cron string for checking stream status
	DefaultCheckStreamCronString = "*/10 * * * *" // Every 10 minutes
	// Base X11 display number; each output gets BaseDisplayNumber + index
	BaseDisplayNumber = 100
)

// StreamOutput represents a single video track configuration parsed from STREAM_OUTPUTS JSON.
type StreamOutput struct {
	Resolution   string `json:"resolution,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	Framerate    int    `json:"framerate,omitempty"`
	VideoBitrate string `json:"videoBitrate,omitempty"`
	Name         string `json:"name,omitempty"`
	Display      int    `json:"-"`
}

// Config holds the application-wide streaming configuration.
type Config struct {
	WebpageURL string
	RTMPURL    string
	Outputs    []StreamOutput
}

// StreamState represents the current state of the stream, tracking all processes
// involved (Xvfb displays, Chrome instances, FFmpeg) for coordinated teardown.
type StreamState struct {
	mu            sync.RWMutex
	isRunning     bool
	cancelFunc    context.CancelFunc
	chromeCancels []context.CancelFunc
	xvfbCmds      []*exec.Cmd
	ffmpegCmd     *exec.Cmd
}

// Health response structure
type Health struct {
	Uptime  time.Duration
	Message string
	Date    time.Time
}

var (
	// Global stream state shared across the application
	globalStreamState = &StreamState{}
	// When the application started; used for health checks
	startTime = time.Now()
)

// setStreamRunning marks the stream as running and saves all process handles for later teardown.
func (s *StreamState) setStreamRunning(cancelFunc context.CancelFunc, chromeCancels []context.CancelFunc, xvfbCmds []*exec.Cmd, ffmpegCmd *exec.Cmd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isRunning = true
	s.cancelFunc = cancelFunc
	s.chromeCancels = chromeCancels
	s.xvfbCmds = xvfbCmds
	s.ffmpegCmd = ffmpegCmd
}

// stopStream ends the current stream if it's running: cancels Chrome contexts,
// terminates FFmpeg gracefully, then kills all Xvfb processes.
func (s *StreamState) stopStream(logger *zap.Logger) {
	s.mu.Lock()

	if !s.isRunning {
		s.mu.Unlock()
		return
	}

	// Capture handles and clear state under lock, then release before slow work
	chromeCancels := s.chromeCancels
	ffmpegCmd := s.ffmpegCmd
	xvfbCmds := s.xvfbCmds
	cancelFunc := s.cancelFunc

	s.isRunning = false
	s.cancelFunc = nil
	s.chromeCancels = nil
	s.xvfbCmds = nil
	s.ffmpegCmd = nil
	s.mu.Unlock()

	logger.Info("Stopping existing stream...")

	for i, cancel := range chromeCancels {
		if cancel != nil {
			logger.Debug("Cancelling Chrome context", zap.Int("index", i))
			cancel()
		}
	}

	if ffmpegCmd != nil && ffmpegCmd.Process != nil {
		logger.Debug("Sending SIGTERM to FFmpeg process")
		_ = ffmpegCmd.Process.Signal(syscall.SIGTERM)

		// Give FFmpeg a moment to flush and exit, then force-kill.
		// We don't call Wait() here because startFFmpegStream owns that.
		time.Sleep(5 * time.Second)
		// If the process is still running, force-kill it
		_ = ffmpegCmd.Process.Kill()
	}

	for i, cmd := range xvfbCmds {
		if cmd != nil && cmd.Process != nil {
			logger.Debug("Killing Xvfb process", zap.Int("display", BaseDisplayNumber+i))
			_ = cmd.Process.Kill()
			go func(c *exec.Cmd) { _, _ = c.Process.Wait() }(cmd)
		}
	}

	if cancelFunc != nil {
		logger.Debug("Cancelling stream context")
		cancelFunc()
	}

	logger.Info("Existing stream stopped")
}

// isStreamRunning returns whether the stream is currently active.
func (s *StreamState) isStreamRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// RestartStream stops the current stream; the main loop will automatically restart it.
func RestartStream(ctx context.Context, config *Config) error {
	logger := utils.GetLoggerFromContext(ctx)
	logger.Info("Triggering stream restart...")
	globalStreamState.stopStream(logger)
	return nil
}

// IsStreamRunning checks if the stream is currently running.
func IsStreamRunning() bool {
	return globalStreamState.isStreamRunning()
}

// StopCurrentStream stops the current stream if it's running.
func StopCurrentStream(ctx context.Context) {
	logger := utils.GetLoggerFromContext(ctx)
	globalStreamState.stopStream(logger)
}

func main() {
	logger := utils.GetLogger()
	ctx := utils.SaveLoggerToContext(context.Background(), logger)

	config, err := loadConfig(ctx)
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	logger.Debug("Starting webpage stream capture",
		zap.String("webpage", config.WebpageURL),
		zap.String("rtmp", config.RTMPURL),
		zap.Int("numOutputs", len(config.Outputs)))

	for i, out := range config.Outputs {
		logger.Debug("Output configuration",
			zap.Int("index", i),
			zap.Int("width", out.Width),
			zap.Int("height", out.Height),
			zap.Int("framerate", out.Framerate),
			zap.String("videoBitrate", out.VideoBitrate),
			zap.String("name", out.Name))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	serverPort := utils.GetEnvOrDefault("PORT", "8080")
	serverAddress := "0.0.0.0:" + serverPort
	logger.Info("Starting HTTP server", zap.String("address", serverAddress))

	setupHTTPRoutes()

	server := &http.Server{
		Addr: serverAddress,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", zap.Error(err))
		}
	}()

	cronScheduler := setupStreamStatusChecker(ctx, config)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Info("Received shutdown signal, stopping...")
		signal.Stop(c)
		StopCurrentStream(ctx)
		if cronScheduler != nil {
			cronScheduler.Stop()
		}
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("Failed to shutdown HTTP server", zap.Error(err))
		}
		cancel()
	}()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, exiting...")
			return
		default:
			logger.Info("Starting/restarting stream...")
			if err := streamWebpage(ctx, config); err != nil {
				if ctx.Err() != nil {
					logger.Info("Stream stopped due to context cancellation")
					return
				}
				logger.Info("Stream ended, will restart in 5 seconds", zap.Error(err))
				time.Sleep(5 * time.Second)
			}
		}
	}
}

// getHealthResponse returns the health of the application.
func getHealthResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	data := Health{
		Uptime:  time.Since(startTime),
		Message: "OK",
		Date:    time.Now(),
	}
	json.NewEncoder(w).Encode(data)
}

// setupHTTPRoutes configures HTTP routes for metrics and health checks.
func setupHTTPRoutes() {
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", getHealthResponse)
}

// resolveResolution maps a named resolution preset to width/height dimensions.
func resolveResolution(resolution string) (int, int, error) {
	switch strings.ToLower(resolution) {
	case "360p":
		return 640, 360, nil
	case "720p":
		return 1280, 720, nil
	case "1080p":
		return 1920, 1080, nil
	case "2k":
		return 2560, 1440, nil
	default:
		return 0, 0, fmt.Errorf("unsupported resolution: %s", resolution)
	}
}

// deriveBitrate computes a default video bitrate string based on pixel count and framerate,
// following Twitch broadcasting guidelines.
func deriveBitrate(width, height, framerate int) string {
	pixels := width * height
	highFPS := framerate >= 60

	switch {
	case pixels <= 640*360:
		return "1000k"
	case pixels <= 1280*720:
		if highFPS {
			return "4000k"
		}
		return "3000k"
	case pixels <= 1920*1080:
		if highFPS {
			return "6000k"
		}
		return "4500k"
	default:
		if highFPS {
			return "8500k"
		}
		return "6000k"
	}
}

// loadConfig loads streaming configuration from environment variables.
// If TWITCH_ENHANCED_BROADCASTING is enabled, calls the Go Live API for config.
// Otherwise, if STREAM_OUTPUTS is set, parses multitrack JSON config.
// Falls back to single-output mode using RESOLUTION/FRAMERATE.
func loadConfig(ctx context.Context) (*Config, error) {
	logger := utils.GetLoggerFromContext(ctx)

	config := &Config{
		WebpageURL: utils.GetEnvOrDefault("WEBPAGE_URL", DefaultWebpageURL),
		RTMPURL:    utils.GetEnvOrDefault("RTMP_URL", DefaultRTMPURL),
	}

	// Twitch Enhanced Broadcasting mode: call Go Live API for server-provided config.
	// STREAM_OUTPUTS is parsed first (if set) to derive canvas preferences for the API request,
	// then the API response overrides the actual output tracks.
	enhancedBroadcasting := utils.GetEnvOrDefault("TWITCH_ENHANCED_BROADCASTING", "")
	if strings.EqualFold(enhancedBroadcasting, "true") {
		streamKey := utils.GetEnvOrDefault("TWITCH_STREAM_KEY", "")
		if streamKey == "" {
			return nil, fmt.Errorf("TWITCH_ENHANCED_BROADCASTING is enabled but TWITCH_STREAM_KEY is not set")
		}

		opts := twitch.GoLiveOptions{
			ClientName: utils.GetEnvOrDefault("TWITCH_CLIENT_NAME", ""),
		}

		// Derive canvas preferences from STREAM_OUTPUTS if available
		streamOutputsJSON := utils.GetEnvOrDefault("STREAM_OUTPUTS", "")
		if streamOutputsJSON != "" {
			var hintOutputs []StreamOutput
			if err := json.Unmarshal([]byte(streamOutputsJSON), &hintOutputs); err == nil && len(hintOutputs) > 0 {
				// Resolve resolution presets to dimensions before canvas inference
				for i := range hintOutputs {
					if hintOutputs[i].Resolution != "" && hintOutputs[i].Width == 0 {
						w, h, err := resolveResolution(hintOutputs[i].Resolution)
						if err == nil {
							hintOutputs[i].Width = w
							hintOutputs[i].Height = h
						}
					}
				}
				// Find the largest landscape entry for primary canvas
				for _, o := range hintOutputs {
					if o.Width >= o.Height && o.Width > opts.CanvasWidth {
						opts.CanvasWidth = o.Width
						opts.CanvasHeight = o.Height
						if o.Framerate > opts.Framerate {
							opts.Framerate = o.Framerate
						}
					}
				}
				// Default framerate from env or 30 if none was specified in outputs
				if opts.Framerate == 0 {
					defaultFR, _ := strconv.Atoi(utils.GetEnvOrDefault("FRAMERATE", DefaultFramerate))
					if defaultFR == 0 {
						defaultFR = 30
					}
					opts.Framerate = defaultFR
				}
				// Find the first portrait entry for the portrait canvas
				for _, o := range hintOutputs {
					if o.Height > o.Width {
						opts.PortraitCanvas = &twitch.GoLiveCanvas{
							Width:        o.Width,
							Height:       o.Height,
							CanvasWidth:  o.Width,
							CanvasHeight: o.Height,
							Framerate:    twitch.GoLiveFramerate{Numerator: opts.Framerate, Denominator: 1},
						}
						break
					}
				}
				// Default max tracks to the number of outputs defined
				numOutputs := len(hintOutputs)
				if opts.PortraitCanvas != nil && numOutputs < 4 {
					numOutputs = 4
				}
				opts.MaxTracks = &numOutputs
			}
		}


		logger.Info("Enhanced Broadcasting enabled, calling Twitch Go Live API...",
			zap.Intp("maxTracks", opts.MaxTracks),
			zap.Int("canvasWidth", opts.CanvasWidth),
			zap.Int("canvasHeight", opts.CanvasHeight),
			zap.Int("framerate", opts.Framerate),
			zap.Bool("portrait", opts.PortraitCanvas != nil))
		goLiveResp, err := twitch.CallGoLiveAPI(ctx, streamKey, opts)
		if err != nil {
			logger.Warn("Go Live API call failed, falling back to standard configuration",
				zap.Error(err))
		} else {
			if err := applyGoLiveConfig(ctx, config, goLiveResp); err != nil {
				logger.Warn("Failed to apply Go Live API configuration, falling back",
					zap.Error(err))
			} else {
				logger.Info("Enhanced Broadcasting configured from Go Live API",
					zap.String("configId", goLiveResp.Meta.ConfigID),
					zap.Int("numTracks", len(config.Outputs)),
					zap.String("rtmpURL", config.RTMPURL))
				return config, nil
			}
		}
	}

	streamOutputsJSON := utils.GetEnvOrDefault("STREAM_OUTPUTS", "")

	if streamOutputsJSON != "" {
		var outputs []StreamOutput
		if err := json.Unmarshal([]byte(streamOutputsJSON), &outputs); err != nil {
			return nil, fmt.Errorf("failed to parse STREAM_OUTPUTS JSON: %w", err)
		}
		if len(outputs) == 0 {
			return nil, fmt.Errorf("STREAM_OUTPUTS is set but contains no entries")
		}

		for i := range outputs {
			if outputs[i].Resolution != "" && outputs[i].Width > 0 && outputs[i].Height > 0 {
				logger.Warn("Output specifies both resolution and width/height; explicit dimensions take priority",
					zap.Int("index", i),
					zap.String("resolution", outputs[i].Resolution),
					zap.Int("width", outputs[i].Width),
					zap.Int("height", outputs[i].Height))
			}
			if outputs[i].Resolution != "" && (outputs[i].Width == 0 || outputs[i].Height == 0) {
				w, h, err := resolveResolution(outputs[i].Resolution)
				if err != nil {
					return nil, fmt.Errorf("output %d: %w", i, err)
				}
				outputs[i].Width = w
				outputs[i].Height = h
			}
			if outputs[i].Width == 0 || outputs[i].Height == 0 {
				return nil, fmt.Errorf("output %d: must specify resolution or width/height", i)
			}

			if outputs[i].Framerate == 0 {
				defaultFR, _ := strconv.Atoi(utils.GetEnvOrDefault("FRAMERATE", DefaultFramerate))
				if defaultFR == 0 {
					defaultFR = 30
				}
				outputs[i].Framerate = defaultFR
			}

			if outputs[i].VideoBitrate == "" {
				outputs[i].VideoBitrate = deriveBitrate(outputs[i].Width, outputs[i].Height, outputs[i].Framerate)
			}

			outputs[i].Display = BaseDisplayNumber + i

			if outputs[i].Name == "" {
				outputs[i].Name = fmt.Sprintf("track%d-%dx%d", i, outputs[i].Width, outputs[i].Height)
			}
		}

		config.Outputs = outputs
		logger.Info("Loaded multi-output configuration from STREAM_OUTPUTS", zap.Int("numOutputs", len(outputs)))
	} else {
		resolution := utils.GetEnvOrDefault("RESOLUTION", DefaultResolution)
		framerate := utils.GetEnvOrDefault("FRAMERATE", DefaultFramerate)

		w, h, err := resolveResolution(resolution)
		if err != nil {
			logger.Warn("Unsupported resolution, defaulting to 720p", zap.String("resolution", resolution))
			w, h = 1280, 720
			resolution = "720p"
		}

		fr, err := strconv.Atoi(framerate)
		if err != nil || (fr != 30 && fr != 60) {
			logger.Warn("Unsupported framerate, defaulting to 30fps", zap.String("framerate", framerate))
			fr = 30
		}

		output := StreamOutput{
			Resolution:   resolution,
			Width:        w,
			Height:       h,
			Framerate:    fr,
			VideoBitrate: deriveBitrate(w, h, fr),
			Name:         "primary",
			Display:      BaseDisplayNumber,
		}
		config.Outputs = []StreamOutput{output}
		logger.Info("Using single-output configuration from RESOLUTION/FRAMERATE",
			zap.String("resolution", resolution),
			zap.Int("width", w),
			zap.Int("height", h),
			zap.Int("framerate", fr))
	}

	return config, nil
}

// applyGoLiveConfig converts a Go Live API response into the app's Config,
// setting the RTMP URL and output tracks from the server-provided configuration.
func applyGoLiveConfig(ctx context.Context, config *Config, resp *twitch.GoLiveResponse) error {
	logger := utils.GetLoggerFromContext(ctx)

	if len(resp.IngestEndpoints) == 0 {
		return fmt.Errorf("Go Live API returned no ingest endpoints")
	}
	if len(resp.EncoderConfigurations) == 0 {
		return fmt.Errorf("Go Live API returned no encoder configurations")
	}

	// Find the first RTMP endpoint
	var endpoint *twitch.GoLiveIngestEndpoint
	for i := range resp.IngestEndpoints {
		if strings.EqualFold(resp.IngestEndpoints[i].Protocol, "RTMP") {
			endpoint = &resp.IngestEndpoints[i]
			break
		}
	}
	if endpoint == nil {
		return fmt.Errorf("Go Live API returned no RTMP ingest endpoint")
	}

	// Build the RTMP URL: replace {stream_key} with authentication token, append clientConfigId
	rtmpURL := strings.Replace(endpoint.URLTemplate, "{stream_key}", endpoint.Authentication, 1)
	if resp.Meta.ConfigID != "" {
		if strings.Contains(rtmpURL, "?") {
			rtmpURL += "&clientConfigId=" + resp.Meta.ConfigID
		} else {
			rtmpURL += "?clientConfigId=" + resp.Meta.ConfigID
		}
	}
	config.RTMPURL = rtmpURL

	// Convert encoder configurations to StreamOutputs
	outputs := make([]StreamOutput, 0, len(resp.EncoderConfigurations))
	for i, enc := range resp.EncoderConfigurations {
		framerate := 60
		if enc.Framerate != nil && enc.Framerate.Denominator > 0 {
			framerate = (enc.Framerate.Numerator + enc.Framerate.Denominator/2) / enc.Framerate.Denominator
		}

		bitrate := fmt.Sprintf("%dk", enc.Settings.Bitrate)

		output := StreamOutput{
			Width:        enc.Width,
			Height:       enc.Height,
			Framerate:    framerate,
			VideoBitrate: bitrate,
			Name:         fmt.Sprintf("track%d-%dx%d", i, enc.Width, enc.Height),
			Display:      BaseDisplayNumber + i,
		}
		outputs = append(outputs, output)

		logger.Debug("Enhanced Broadcasting track configured",
			zap.Int("index", i),
			zap.Int("width", enc.Width),
			zap.Int("height", enc.Height),
			zap.Int("framerate", framerate),
			zap.String("bitrate", bitrate))
	}

	config.Outputs = outputs

	if resp.Status.Result == "warning" {
		logger.Warn("Go Live API returned a warning",
			zap.String("message", resp.Status.HTMLEnUS))
	}

	return nil
}

// startXvfb launches an Xvfb virtual display for the given output, waits for
// it to become ready, and returns the process handle for lifecycle management.
func startXvfb(ctx context.Context, output StreamOutput) (*exec.Cmd, error) {
	logger := utils.GetLoggerFromContext(ctx)
	displayNum := output.Display
	screenSize := fmt.Sprintf("%dx%dx24", output.Width, output.Height)

	logger.Info("Starting Xvfb", zap.Int("display", displayNum), zap.String("screenSize", screenSize))

	// Remove stale lock/socket files that may remain after a SIGKILL
	lockFile := fmt.Sprintf("/tmp/.X%d-lock", displayNum)
	socketFile := fmt.Sprintf("/tmp/.X11-unix/X%d", displayNum)
	os.Remove(lockFile)
	os.Remove(socketFile)

	cmd := exec.CommandContext(ctx, "Xvfb",
		fmt.Sprintf(":%d", displayNum),
		"-screen", "0", screenSize,
		"-ac",
		"+extension", "GLX",
		"+render",
		"-noreset",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Xvfb on display :%d: %w", displayNum, err)
	}

	displayStr := fmt.Sprintf(":%d", displayNum)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		checkCmd := exec.CommandContext(ctx, "xdpyinfo", "-display", displayStr)
		if err := checkCmd.Run(); err == nil {
			logger.Debug("Xvfb ready", zap.Int("display", displayNum))
			return cmd, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("xvfb on display :%d did not become ready within 15 seconds", displayNum)
}

// streamWebpage orchestrates the full streaming pipeline: launches an Xvfb display
// and Chrome browser per output track, then starts FFmpeg to capture and stream them all.
func streamWebpage(ctx context.Context, config *Config) error {
	logger := utils.GetLoggerFromContext(ctx)

	if globalStreamState.isStreamRunning() {
		logger.Info("Stream is already running, stopping existing stream before restart")
		globalStreamState.stopStream(logger)
		time.Sleep(2 * time.Second)
	}

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	xvfbCmds := make([]*exec.Cmd, 0, len(config.Outputs))
	chromeCancels := make([]context.CancelFunc, 0, len(config.Outputs))

	cleanup := func() {
		for _, cancel := range chromeCancels {
			if cancel != nil {
				cancel()
			}
		}
		for _, cmd := range xvfbCmds {
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
				go func(c *exec.Cmd) { _, _ = c.Process.Wait() }(cmd)
			}
		}
	}

	for i, output := range config.Outputs {
		xvfbCmd, err := startXvfb(streamCtx, output)
		if err != nil {
			cleanup()
			return fmt.Errorf("failed to start Xvfb for output %d: %w", i, err)
		}
		xvfbCmds = append(xvfbCmds, xvfbCmd)

		isPrimary := i == 0
		chromeCancel, err := startChrome(streamCtx, config, output, isPrimary)
		if err != nil {
			cleanup()
			return fmt.Errorf("failed to start Chrome for output %d: %w", i, err)
		}
		chromeCancels = append(chromeCancels, chromeCancel)
	}

	refreshIntervalStr := utils.GetEnvOrDefault("WEBPAGE_REFRESH_INTERVAL", "")
	if refreshIntervalStr != "" {
		refreshInterval, err := strconv.Atoi(refreshIntervalStr)
		if err != nil || refreshInterval <= 0 {
			logger.Warn("Invalid WEBPAGE_REFRESH_INTERVAL value, automatic refresh disabled",
				zap.String("invalidValue", refreshIntervalStr), zap.Error(err))
		} else {
			logger.Info("Automatic browser refresh is configured but requires chromedp context per instance (managed internally)")
		}
	}

	return startFFmpegStream(streamCtx, config, streamCancel, chromeCancels, xvfbCmds)
}

// startChrome launches a Chrome instance bound to the output's Xvfb display.
// The primary instance produces audio; non-primary instances are muted.
func startChrome(ctx context.Context, config *Config, output StreamOutput, isPrimary bool) (context.CancelFunc, error) {
	logger := utils.GetLoggerFromContext(ctx)
	displayStr := fmt.Sprintf(":%d", output.Display)

	logger.Info("Starting Chrome browser",
		zap.String("url", config.WebpageURL),
		zap.Int("display", output.Display),
		zap.Int("width", output.Width),
		zap.Int("height", output.Height),
		zap.Bool("primary", isPrimary))

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("kiosk", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("allow-running-insecure-content", true),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		chromedp.Flag("use-fake-ui-for-media-stream", true),
		chromedp.Flag("use-fake-device-for-media-stream", true),
		chromedp.Flag("alsa-output-device", "pulse"),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("mute-audio", !isPrimary),
		chromedp.Flag("window-position", "0,0"),
		chromedp.Flag("memory-pressure-off", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-features", "TranslateUI,VizDisplayCompositor"),
		chromedp.Flag("aggressive-cache-discard", true),
		chromedp.Env("DISPLAY="+displayStr),
		chromedp.WindowSize(output.Width, output.Height),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	chromeCtx, chromeCancel := chromedp.NewContext(allocCtx)

	combinedCancel := func() {
		chromeCancel()
		allocCancel()
	}

	if err := chromedp.Run(chromeCtx,
		chromedp.Navigate(config.WebpageURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
	); err != nil {
		combinedCancel()
		return nil, fmt.Errorf("failed to navigate to webpage on display :%d: %w", output.Display, err)
	}

	time.Sleep(3 * time.Second)

	refreshIntervalStr := utils.GetEnvOrDefault("WEBPAGE_REFRESH_INTERVAL", "")
	if refreshIntervalStr != "" {
		refreshInterval, err := strconv.Atoi(refreshIntervalStr)
		if err == nil && refreshInterval > 0 {
			go func() {
				ticker := time.NewTicker(time.Duration(refreshInterval) * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if err := chromedp.Run(chromeCtx, chromedp.Reload()); err != nil {
							logger.Error("Failed to refresh browser page",
								zap.Int("display", output.Display), zap.Error(err))
						}
					case <-ctx.Done():
						return
					}
				}
			}()
		}
	}

	logger.Debug("Chrome started successfully", zap.Int("display", output.Display))
	return combinedCancel, nil
}

// extractNumberFromBitrate extracts the numeric value from a bitrate string (e.g., "3000k" -> 3000).
func extractNumberFromBitrate(bitrate string) int {
	numStr := strings.TrimSuffix(bitrate, "k")
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 3000
	}
	return num
}

// startFFmpegStream builds and runs the FFmpeg command that captures all Xvfb
// displays (one x11grab input per track) plus one audio input, and muxes them
// into a single Enhanced RTMP multitrack FLV stream.
func startFFmpegStream(ctx context.Context, config *Config, streamCancel context.CancelFunc, chromeCancels []context.CancelFunc, xvfbCmds []*exec.Cmd) error {
	logger := utils.GetLoggerFromContext(ctx)
	logger.Info("Starting FFmpeg stream", zap.Int("numTracks", len(config.Outputs)))

	var args []string

	for _, output := range config.Outputs {
		args = append(args,
			"-thread_queue_size", "512",
			"-f", "x11grab",
			"-draw_mouse", "0",
			"-video_size", fmt.Sprintf("%dx%d", output.Width, output.Height),
			"-framerate", strconv.Itoa(output.Framerate),
			"-i", fmt.Sprintf(":%d+0,0", output.Display),
		)
	}

	args = append(args,
		"-thread_queue_size", "512",
		"-f", "alsa",
		"-i", "default",
	)

	audioInputIdx := len(config.Outputs)

	for i := range config.Outputs {
		args = append(args, "-map", fmt.Sprintf("%d:v", i))
	}
	args = append(args, "-map", fmt.Sprintf("%d:a", audioInputIdx))

	encoderPreset := utils.GetEnvOrDefault("ENCODER_PRESET", "ultrafast")
	// Divide available cores among encoder instances to prevent thread oversubscription.
	// Too many threads (e.g. -threads 0 with 4 encoders on 32 cores = 128+ threads)
	// causes context-switch overhead that tanks throughput.
	numOutputs := len(config.Outputs)
	threadsPerEncoder := 4
	if numOutputs > 0 {
		numCPU := runtime.NumCPU()
		threadsPerEncoder = numCPU / numOutputs
		if threadsPerEncoder < 2 {
			threadsPerEncoder = 2
		}
		if threadsPerEncoder > 8 {
			threadsPerEncoder = 8
		}
	}

	args = append(args,
		"-c:v", "libx264",
		"-preset", encoderPreset,
		"-pix_fmt", "yuv420p",
	)

	for i, output := range config.Outputs {
		bitrate := output.VideoBitrate
		bufsize := fmt.Sprintf("%dk", extractNumberFromBitrate(bitrate)*2)
		gopSize := output.Framerate * 2

		args = append(args,
			fmt.Sprintf("-threads:v:%d", i), strconv.Itoa(threadsPerEncoder),
			fmt.Sprintf("-b:v:%d", i), bitrate,
			fmt.Sprintf("-maxrate:v:%d", i), bitrate,
			fmt.Sprintf("-bufsize:v:%d", i), bufsize,
			fmt.Sprintf("-g:v:%d", i), strconv.Itoa(gopSize),
		)
	}

	args = append(args,
		"-c:a", "aac",
		"-b:a", "160k",
		"-ar", "44100",
		"-f", "flv",
		config.RTMPURL,
	)

	zapWriter := &zapio.Writer{Log: logger, Level: zap.DebugLevel}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = zapWriter
	cmd.Stderr = zapWriter

	logger.Debug("Starting FFmpeg with command", zap.Strings("args", args))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if cmd.Process != nil && cmd.ProcessState == nil {
					zapWriter.Sync()
				} else {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	globalStreamState.setStreamRunning(streamCancel, chromeCancels, xvfbCmds, cmd)

	logger.Debug("FFmpeg started successfully, streaming...")

	err := cmd.Wait()

	defer func() {
		globalStreamState.mu.Lock()
		globalStreamState.isRunning = false
		globalStreamState.cancelFunc = nil
		globalStreamState.chromeCancels = nil
		globalStreamState.xvfbCmds = nil
		globalStreamState.ffmpegCmd = nil
		globalStreamState.mu.Unlock()
	}()

	if ctx.Err() != nil {
		logger.Info("Stream stopped due to context cancellation")
		return nil
	}

	return err
}

// setupStreamStatusChecker sets up a cron job to check if the stream is still live.
// If the stream is not live (e.g., platform max duration exceeded), it triggers a restart.
// This is only active when TWITCH_CHANNEL is configured.
func setupStreamStatusChecker(ctx context.Context, config *Config) *cron.Cron {
	logger := utils.GetLoggerFromContext(ctx)

	logger.Debug("Setting up stream status checker")

	twitchChannel := utils.GetEnvOrDefault("TWITCH_CHANNEL", "")
	if twitchChannel != "" {
		logger.Info("Setting up stream status checker for Twitch channel", zap.String("channel", twitchChannel))

		cronString := utils.GetEnvOrDefault("STATUS_CRON_SCHEDULE", DefaultCheckStreamCronString)
		if _, err := cron.ParseStandard(cronString); err != nil {
			logger.Error("Invalid status cron schedule string, using default", zap.String("cronString", cronString), zap.Error(err))
			cronString = DefaultCheckStreamCronString
		}
		logger.Debug("Using cron schedule for stream status checker", zap.String("cronString", cronString))

		c := cron.New()
		c.AddFunc(cronString, func() {
			logger.Info("Checking Twitch stream status", zap.String("channel", twitchChannel))

			client := twitch.GetClient(ctx)
			resp, err := client.GetStreams(&helix.StreamsParams{
				UserLogins: []string{twitchChannel},
			})
			if err != nil {
				logger.Error("Failed to get Twitch stream status", zap.Error(err))
				return
			}

			if len(resp.Data.Streams) == 0 {
				logger.Warn("Stream is not live, restarting...")
				if err := RestartStream(ctx, config); err != nil {
					logger.Error("Failed to restart stream", zap.Error(err))
				}
			} else {
				logger.Info("Stream is live", zap.String("title", resp.Data.Streams[0].Title))
			}
		})
		c.Start()
		logger.Info("Stream status checker started", zap.String("cronString", cronString))
		return c
	} else {
		logger.Debug("Stream status checker not configured, skipping setup")
	}
	return nil
}
