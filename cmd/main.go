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

	"github.com/Zozman/stream-website/utils"
)

const (
	DefaultResolution = "720p"
	DefaultRTMPURL    = "rtmp://localhost:1935/live/stream"
	DefaultWebsiteURL = "https://google.com"
	DefaultFramerate  = "30"
)

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

func startFFmpegStream(ctx context.Context, config *Config, display string) error {
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

	logger.Info("FFmpeg started successfully, streaming...")

	// Wait for the command to finish or context to be cancelled
	err = cmd.Wait()
	if ctx.Err() != nil {
		logger.Info("Stream stopped due to context cancellation")
		return nil
	}

	return err
}
