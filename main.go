package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapio"
)

const loggerKey string = "logger"

// Helper function to get logger from context
func getLogger(ctx context.Context) *zap.Logger {
	if logger, ok := ctx.Value(loggerKey).(*zap.Logger); ok {
		return logger
	}
	// Fallback to a basic logger if none in context
	logger, _ := zap.NewProduction()
	return logger
}

const (
	DefaultResolution = "720p"
	DefaultRTMPURL    = "rtmp://localhost:1935/live/stream"
	DefaultWebsiteURL = "https://google.com"
	DefaultFramerate  = "30"
	DefaultLogLevel   = "info"
	DefaultLogFormat  = "json"
)

type Config struct {
	WebsiteURL string
	RTMPURL    string
	Resolution string
	Framerate  string
	LogLevel   string
	LogFormat  string
	Width      int
	Height     int
}

func main() {
	// Get basic log configuration from environment
	logLevel := getEnvOrDefault("LOG_LEVEL", DefaultLogLevel)
	logFormat := getEnvOrDefault("LOG_FORMAT", DefaultLogFormat)

	// Initialize logger first
	logger, err := initializeLogger(logLevel, logFormat)
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize logger: %v", err))
	}
	defer logger.Sync()

	// Create context with logger
	ctx := context.WithValue(context.Background(), loggerKey, logger)

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
		zap.String("logLevel", config.LogLevel),
		zap.String("logFormat", config.LogFormat),
		zap.Int("width", config.Width),
		zap.Int("height", config.Height))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Handle graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Info("Received shutdown signal, stopping...")
		cancel()
	}()

	if err := streamWebsite(ctx, config); err != nil {
		logger.Fatal("Failed to stream website", zap.Error(err))
	}
}

func initializeLogger(logLevel, logFormat string) (*zap.Logger, error) {
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

func loadConfig(ctx context.Context) (*Config, error) {
	logger := getLogger(ctx)

	config := &Config{
		WebsiteURL: getEnvOrDefault("WEBSITE_URL", DefaultWebsiteURL),
		RTMPURL:    getEnvOrDefault("RTMP_URL", DefaultRTMPURL),
		Resolution: getEnvOrDefault("RESOLUTION", DefaultResolution),
		Framerate:  getEnvOrDefault("FRAMERATE", DefaultFramerate),
		LogLevel:   getEnvOrDefault("LOG_LEVEL", DefaultLogLevel),
		LogFormat:  getEnvOrDefault("LOG_FORMAT", DefaultLogFormat),
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

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func streamWebsite(ctx context.Context, config *Config) error {
	logger := getLogger(ctx)

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

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	chromeCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

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
	return startFFmpegStream(ctx, config, displayInfo)
}

func getDisplayInfo() (string, error) {
	// Try to get the DISPLAY environment variable
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":0" // Default X11 display
	}
	return display, nil
}

func startFFmpegStream(ctx context.Context, config *Config, display string) error {
	logger := getLogger(ctx)

	logger.Info("Starting FFmpeg stream")

	// Calculate keyframe interval for 2 seconds (GOP size = framerate * 2)
	framerate := config.Framerate
	framerateInt, err := strconv.Atoi(framerate)
	if err != nil {
		logger.Error("Invalid framerate, defaulting to 30", zap.String("framerate", framerate), zap.Error(err))
		framerateInt = 30 // Default to 30
	}
	keyframeInterval := fmt.Sprintf("%d", framerateInt*2)

	// FFmpeg command to capture screen and audio, then stream to RTMP
	args := []string{
		"-f", "x11grab",
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height),
		"-framerate", config.Framerate,
		"-i", fmt.Sprintf("%s+0,0", display), // Specify exact offse
		"-f", "alsa", // Use ALSA for audio capture (FFmpeg supports this)
		"-i", "default", // Use ALSA default device (configured to route to PulseAudio)
		"-vf", "crop=in_w:in_h:0:0", // Crop to exact dimensions
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-crf", "23",
		"-maxrate", "3000k",
		"-bufsize", "6000k",
		"-pix_fmt", "yuv420p",
		"-g", keyframeInterval, // Set GOP size for 2-second keyframe interval
		"-c:a", "aac",
		"-b:a", "128k",
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

	logger.Info("FFmpeg started successfully, streaming...")

	// Wait for the command to finish or context to be cancelled
	err = cmd.Wait()
	if ctx.Err() != nil {
		logger.Info("Stream stopped due to context cancellation")
		return nil
	}

	return err
}
