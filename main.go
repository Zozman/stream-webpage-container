package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"go.uber.org/zap"
)

const (
	DefaultResolution = "1080p"
	DefaultRTMPURL    = "rtmp://localhost:1935/live/stream"
	DefaultWebsiteURL = "https://example.com"
)

type Config struct {
	WebsiteURL string
	RTMPURL    string
	Resolution string
	Width      int
	Height     int
	Logger     *zap.Logger
}

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize logger: %v", err))
	}
	defer logger.Sync()

	config := loadConfig(logger)

	logger.Info("Starting website stream capture",
		zap.String("website", config.WebsiteURL),
		zap.String("rtmp", config.RTMPURL),
		zap.String("resolution", config.Resolution),
		zap.Int("width", config.Width),
		zap.Int("height", config.Height))

	ctx, cancel := context.WithCancel(context.Background())
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

func loadConfig(logger *zap.Logger) *Config {
	config := &Config{
		WebsiteURL: getEnvOrDefault("WEBSITE_URL", DefaultWebsiteURL),
		RTMPURL:    getEnvOrDefault("RTMP_URL", DefaultRTMPURL),
		Resolution: getEnvOrDefault("RESOLUTION", DefaultResolution),
		Logger:     logger,
	}

	// Set resolution dimensions
	switch strings.ToLower(config.Resolution) {
	case "720p":
		config.Width = 1280
		config.Height = 720
	case "1080p":
		config.Width = 1920
		config.Height = 1080
	default:
		logger.Warn("Unsupported resolution, defaulting to 1080p", zap.String("resolution", config.Resolution))
		config.Resolution = "1080p"
		config.Width = 1920
		config.Height = 1080
	}

	return config
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func streamWebsite(ctx context.Context, config *Config) error {
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
		chromedp.WindowSize(config.Width, config.Height),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	chromeCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Start Chrome and navigate to website
	config.Logger.Info("Starting Chrome browser", zap.String("url", config.WebsiteURL))

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

	config.Logger.Info("Display information", zap.String("display", displayInfo))

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
	config.Logger.Info("Starting FFmpeg stream")

	// FFmpeg command to capture screen and audio, then stream to RTMP
	args := []string{
		"-f", "x11grab",
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height),
		"-framerate", "30",
		"-i", display,
		"-f", "alsa", // Use ALSA for audio capture (FFmpeg supports this)
		"-i", "default", // Use ALSA default device (configured to route to PulseAudio)
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-crf", "23",
		"-maxrate", "3000k",
		"-bufsize", "6000k",
		"-pix_fmt", "yuv420p",
		"-g", "60",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "44100",
		"-f", "flv",
		config.RTMPURL,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	config.Logger.Info("Starting FFmpeg with command", zap.Strings("args", args))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	config.Logger.Info("FFmpeg started successfully, streaming...")

	// Wait for the command to finish or context to be cancelled
	err := cmd.Wait()
	if ctx.Err() != nil {
		config.Logger.Info("Stream stopped due to context cancellation")
		return nil
	}

	return err
}
