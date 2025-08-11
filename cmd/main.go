package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
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

	"github.com/Zozman/stream-website/twitch"
	"github.com/Zozman/stream-website/utils"
)

const (
	DefaultResolution            = "720p"
	DefaultRTMPURL               = "rtmp://localhost:1935/live/stream"
	DefaultWebsiteURL            = "https://google.com"
	DefaultFramerate             = "30"
	DefaultCheckStreamCronString = "*/10 * * * *" // Every 10 minutes
)

// StreamState holds the current stream state
type StreamState struct {
	mu           sync.RWMutex
	isRunning    bool
	cancelFunc   context.CancelFunc
	chromeCancel context.CancelFunc
	ffmpegCmd    *exec.Cmd
}

// Health response structure
type Health struct {
	Uptime  time.Duration
	Message string
	Date    time.Time
}

var (
	globalStreamState = &StreamState{}
	startTime         = time.Now()
)

// setStreamRunning sets the stream as running with the given cancel functions and command
func (s *StreamState) setStreamRunning(cancelFunc, chromeCancel context.CancelFunc, ffmpegCmd *exec.Cmd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isRunning = true
	s.cancelFunc = cancelFunc
	s.chromeCancel = chromeCancel
	s.ffmpegCmd = ffmpegCmd
}

// stopStream stops the current stream if it's running
func (s *StreamState) stopStream(logger *zap.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return
	}

	logger.Info("Stopping existing stream...")

	// Stop FFmpeg process
	if s.ffmpegCmd != nil && s.ffmpegCmd.Process != nil {
		logger.Debug("Terminating FFmpeg process")
		if err := s.ffmpegCmd.Process.Kill(); err != nil {
			logger.Warn("Failed to kill FFmpeg process", zap.Error(err))
		}
	}

	// Cancel Chrome context
	if s.chromeCancel != nil {
		logger.Debug("Cancelling Chrome context")
		s.chromeCancel()
	}

	// Cancel main stream context
	if s.cancelFunc != nil {
		logger.Debug("Cancelling stream context")
		s.cancelFunc()
	}

	s.isRunning = false
	s.cancelFunc = nil
	s.chromeCancel = nil
	s.ffmpegCmd = nil

	logger.Info("Existing stream stopped")
}

// isStreamRunning returns whether a stream is currently running
func (s *StreamState) isStreamRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// RestartStream stops any existing stream and lets the main loop restart it
func RestartStream(ctx context.Context, config *Config) error {
	logger := utils.GetLoggerFromContext(ctx)
	logger.Info("Triggering stream restart...")

	// Stop the current stream - the main loop will automatically restart it
	globalStreamState.stopStream(logger)

	return nil
}

// IsStreamRunning returns whether a stream is currently active
func IsStreamRunning() bool {
	return globalStreamState.isStreamRunning()
}

// StopCurrentStream stops any currently running stream
func StopCurrentStream(ctx context.Context) {
	logger := utils.GetLoggerFromContext(ctx)
	globalStreamState.stopStream(logger)
}

type Config struct {
	WebsiteURL string
	RTMPURL    string
	Resolution string
	Framerate  string
	Width      int
	Height     int
}

func main() {
	logger := utils.GetLogger()

	// Create context with logger
	ctx := utils.SaveLoggerToContext(context.Background(), logger)

	// Load configuration with logging available
	config, err := loadConfig(ctx)
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	logger.Info("Starting website stream capture",
		zap.String("website", config.WebsiteURL),
		zap.String("rtmp", config.RTMPURL),
		zap.String("resolution", config.Resolution),
		zap.String("framerate", config.Framerate),
		zap.Int("width", config.Width),
		zap.Int("height", config.Height))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Setup HTTP server for metrics and health checks
	serverPort := utils.GetEnvOrDefault("PORT", "8080")
	serverAddress := "0.0.0.0:" + serverPort
	logger.Info("Starting HTTP server", zap.String("address", serverAddress))

	// Setup HTTP routes
	setupHTTPRoutes()

	// Start HTTP server in a goroutine
	server := &http.Server{
		Addr: serverAddress,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", zap.Error(err))
		}
	}()

	// Setup stream status checker (will start monitoring after stream begins)
	setupStreamStatusChecker(ctx, config)

	// Handle graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Info("Received shutdown signal, stopping...")

		// Stop current stream if running
		StopCurrentStream(ctx)

		// Shutdown HTTP server
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("Failed to shutdown HTTP server", zap.Error(err))
		}

		cancel()
	}()

	// Run the stream in a loop to handle restarts from the cron job or manual restarts
	for {
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, exiting...")
			return
		default:
			logger.Info("Starting/restarting stream...")
			if err := streamWebsite(ctx, config); err != nil {
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

// Returns the health of the application
func getHealthResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	data := Health{
		Uptime:  time.Since(startTime),
		Message: "OK",
		Date:    time.Now(),
	}
	json.NewEncoder(w).Encode(data)
}

// setupHTTPRoutes configures the HTTP endpoints
func setupHTTPRoutes() {
	// Setup prometheus metrics
	http.Handle("/metrics", promhttp.Handler())
	// Setup health endpoint
	http.HandleFunc("/health", getHealthResponse)
}

func loadConfig(ctx context.Context) (*Config, error) {
	logger := utils.GetLoggerFromContext(ctx)

	config := &Config{
		WebsiteURL: utils.GetEnvOrDefault("WEBSITE_URL", DefaultWebsiteURL),
		RTMPURL:    utils.GetEnvOrDefault("RTMP_URL", DefaultRTMPURL),
		Resolution: utils.GetEnvOrDefault("RESOLUTION", DefaultResolution),
		Framerate:  utils.GetEnvOrDefault("FRAMERATE", DefaultFramerate),
	}

	// Validate and set framerate
	originalFramerate := config.Framerate
	switch config.Framerate {
	case "30", "60":
		logger.Debug("Using framerate", zap.String("framerate", config.Framerate))
	default:
		logger.Warn("Unsupported framerate, defaulting to 30fps", zap.String("framerate", originalFramerate))
		config.Framerate = "30"
	}

	// Validate and set resolution dimensions
	originalResolution := config.Resolution
	switch strings.ToLower(config.Resolution) {
	case "720p":
		config.Width = 1280
		config.Height = 720
		logger.Debug("Using resolution", zap.String("resolution", config.Resolution), zap.Int("width", config.Width), zap.Int("height", config.Height))
	case "1080p":
		config.Width = 1920
		config.Height = 1080
		logger.Debug("Using resolution", zap.String("resolution", config.Resolution), zap.Int("width", config.Width), zap.Int("height", config.Height))
	case "2k":
		config.Width = 2560
		config.Height = 1440
		logger.Debug("Using resolution", zap.String("resolution", config.Resolution), zap.Int("width", config.Width), zap.Int("height", config.Height))
	default:
		logger.Warn("Unsupported resolution, defaulting to 720p", zap.String("resolution", originalResolution))
		config.Resolution = "720p"
		config.Width = 1280
		config.Height = 720
	}

	return config, nil
}

func streamWebsite(ctx context.Context, config *Config) error {
	logger := utils.GetLoggerFromContext(ctx)

	// Check if a stream is already running and stop it
	if globalStreamState.isStreamRunning() {
		logger.Info("Stream is already running, stopping existing stream before restart")
		globalStreamState.stopStream(logger)
		// Give some time for cleanup
		time.Sleep(2 * time.Second)
	}

	// Create a cancellable context for this stream session
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	// Create Chrome context with options for screen capture
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false), // We need non-headless for video capture
		chromedp.Flag("kiosk", true),
		chromedp.Flag("disable-gpu", false),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("allow-running-insecure-content", true),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		chromedp.Flag("use-fake-ui-for-media-stream", true),
		chromedp.Flag("use-fake-device-for-media-stream", true),
		chromedp.Flag("alsa-output-device", "pulse"),
		chromedp.Flag("enable-features", "VaapiVideoDecoder"),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("mute-audio", false),
		chromedp.Flag("window-position", "0,0"),
		chromedp.WindowSize(config.Width, config.Height),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(streamCtx, opts...)
	defer allocCancel()

	chromeCtx, chromeCancel := chromedp.NewContext(allocCtx)
	defer chromeCancel()

	// Start Chrome and navigate to website
	logger.Info("Starting Chrome browser", zap.String("url", config.WebsiteURL))

	if err := chromedp.Run(chromeCtx,
		chromedp.Navigate(config.WebsiteURL),
		chromedp.WaitVisible("body", chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("failed to navigate to website: %v", err)
	}

	// Wait a moment for the page to fully load
	time.Sleep(3 * time.Second)

	// Get the display information to find where Chrome is running
	displayInfo, err := getDisplayInfo()
	if err != nil {
		return fmt.Errorf("failed to get display info: %v", err)
	}

	logger.Debug("Display information", zap.String("display", displayInfo))

	// Start FFmpeg to capture and stream
	return startFFmpegStream(streamCtx, config, displayInfo, streamCancel, chromeCancel)
}

func getDisplayInfo() (string, error) {
	// Try to get the DISPLAY environment variable
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":0" // Default X11 display
	}
	return display, nil
}

// Helper function to extract numeric value from bitrate string (e.g., "3000k" -> 3000)
func extractNumberFromBitrate(bitrate string) int {
	// Remove the 'k' suffix and convert to int
	numStr := strings.TrimSuffix(bitrate, "k")
	num, err := strconv.Atoi(numStr)
	if err != nil {
		return 3000 // Default fallback
	}
	return num
}

func startFFmpegStream(ctx context.Context, config *Config, display string, streamCancel, chromeCancel context.CancelFunc) error {
	logger := utils.GetLoggerFromContext(ctx)

	logger.Info("Starting FFmpeg stream")

	// Calculate keyframe interval for 2 seconds (GOP size = framerate * 2)
	framerate := config.Framerate
	framerateInt, err := strconv.Atoi(framerate)
	if err != nil {
		logger.Error("Invalid framerate, defaulting to 30", zap.String("framerate", framerate), zap.Error(err))
		framerateInt = 30 // Default to 30
	}
	keyframeInterval := fmt.Sprintf("%d", framerateInt*2)

	// Set bitrate based on Twitch recommendations for resolution and framerate
	// References: https://help.twitch.tv/s/article/broadcasting-guidelines?language=en_US
	//             https://help.twitch.tv/s/article/stream-quality?language=en_US#how-to-stream
	var videoBitrate string
	audioBitrate := "160k" // Always use 160k for audio

	switch strings.ToLower(config.Resolution) {
	case "720p":
		if framerateInt >= 60 {
			videoBitrate = "4000k" // 720p 60fps: 4000 kbps
		} else {
			videoBitrate = "3000k" // 720p 30fps: 3000 kbps
		}
	case "1080p":
		if framerateInt >= 60 {
			videoBitrate = "6000k" // 1080p 60fps: 6000 kbps
		} else {
			videoBitrate = "4500k" // 1080p 30fps: 4500 kbps
		}
	case "2k":
		if framerateInt >= 60 {
			videoBitrate = "8500k" // 2K 60fps: 8500 kbps (Twitch max for non-partners)
		} else {
			videoBitrate = "6000k" // 2K 30fps: 6000 kbps
		}
	default:
		// Default to 720p 30fps settings
		videoBitrate = "3000k"
	}

	// Buffer size should be 2x the video bitrate
	bufferSize := fmt.Sprintf("%dk", (extractNumberFromBitrate(videoBitrate) * 2))

	logger.Debug("Starting Stream Using FFmpeg",
		zap.String("resolution", config.Resolution),
		zap.String("framerate", config.Framerate),
		zap.String("videoBitrate", videoBitrate),
		zap.String("audioBitrate", audioBitrate),
		zap.String("bufferSize", bufferSize))

	// FFmpeg command to capture screen and audio, then stream to RTMP
	args := []string{
		"-f", "x11grab",
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height),
		"-framerate", config.Framerate,
		"-i", fmt.Sprintf("%s+0,0", display), // Specify exact offset
		"-f", "alsa", // Use ALSA for audio capture (FFmpeg supports this)
		"-i", "default", // Use ALSA default device (configured to route to PulseAudio)
		"-vf", "crop=in_w:in_h:0:0", // Crop to exact dimensions
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-crf", "23",
		"-maxrate", videoBitrate,
		"-bufsize", bufferSize,
		"-pix_fmt", "yuv420p",
		"-g", keyframeInterval, // Set GOP size for 2-second keyframe interval
		"-c:a", "aac",
		"-b:a", audioBitrate,
		"-ar", "44100",
		"-f", "flv",
		config.RTMPURL,
	}

	zapWriter := &zapio.Writer{Log: logger, Level: zap.DebugLevel}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = zapWriter
	cmd.Stderr = zapWriter

	logger.Info("Starting FFmpeg with command", zap.Strings("args", args))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	// Register this stream as running
	globalStreamState.setStreamRunning(streamCancel, chromeCancel, cmd)

	logger.Info("FFmpeg started successfully, streaming...")

	// Wait for the command to finish or context to be cancelled
	err = cmd.Wait()

	// Clean up stream state when done
	defer func() {
		globalStreamState.mu.Lock()
		globalStreamState.isRunning = false
		globalStreamState.cancelFunc = nil
		globalStreamState.chromeCancel = nil
		globalStreamState.ffmpegCmd = nil
		globalStreamState.mu.Unlock()
	}()

	if ctx.Err() != nil {
		logger.Info("Stream stopped due to context cancellation")
		return nil
	}

	return err
}

// If the proper enviromental variables are set, setup a cron job to check the status of the stream
// If the stream is not live, then restart the stream
// This is used because various platforms have maximum stream durations and after that we need to restart
func setupStreamStatusChecker(ctx context.Context, config *Config) {
	logger := utils.GetLoggerFromContext(ctx)

	logger.Debug("Setting up stream status checker")

	// If a TWITCH_CHANNEL environment variable is set, we assume we want to check the stream status
	twitchChannel := utils.GetEnvOrDefault("TWITCH_CHANNEL", "")
	if twitchChannel != "" {
		logger.Info("Setting up stream status checker for Twitch channel", zap.String("channel", twitchChannel))

		// Get and validate the cron string from environment variables or use the default
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
	} else {
		logger.Debug("Stream status checker not configured, skipping setup")
	}
}
