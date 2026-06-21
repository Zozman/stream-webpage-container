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

	"github.com/Zozman/stream-webpage-container/twitch"
	"github.com/Zozman/stream-webpage-container/utils"
)

const (
	// Default RTMP URL to stream to
	DefaultRTMPURL = "rtmp://localhost:1935/live/stream"
	// Default webpage to capture
	DefaultWebpageURL = "https://google.com"
	// Default cron string for checking stream status
	DefaultCheckStreamCronString = "*/10 * * * *" // Every 10 minutes
)

// Struct that represents the current state of the stream
type StreamState struct {
	mu             sync.RWMutex
	isRunning      bool
	cancelFunc     context.CancelFunc
	browserCancels []context.CancelFunc
	ffmpegCmd      *exec.Cmd
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

// Function to set that the current stream is running and save the right objects into the StreamState
func (s *StreamState) setStreamRunning(cancelFunc context.CancelFunc, browserCancels []context.CancelFunc, ffmpegCmd *exec.Cmd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isRunning = true
	s.cancelFunc = cancelFunc
	s.browserCancels = append([]context.CancelFunc(nil), browserCancels...)
	s.ffmpegCmd = ffmpegCmd
}

// Function to end the current stream if it's running
func (s *StreamState) stopStream(logger *zap.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return
	}

	logger.Info("Stopping existing stream...")

	// Cancel active browser sessions
	for index, browserCancel := range s.browserCancels {
		if browserCancel != nil {
			logger.Debug("Cancelling browser context", zap.Int("index", index))
			browserCancel()
		}
	}

	// Ask FFmpeg to terminate gracefully
	if s.ffmpegCmd != nil && s.ffmpegCmd.Process != nil {
		logger.Debug("Sending SIGTERM to FFmpeg process")
		_ = s.ffmpegCmd.Process.Signal(syscall.SIGTERM)

		done := make(chan struct{})
		go func(cmd *exec.Cmd) {
			// Wait will reap the process and free OS resources
			_, _ = cmd.Process.Wait()
			close(done)
		}(s.ffmpegCmd)

		select {
		case <-done:
			logger.Debug("FFmpeg exited gracefully")
		case <-time.After(5 * time.Second):
			logger.Warn("FFmpeg did not exit in time, killing")
			_ = s.ffmpegCmd.Process.Kill()
			// Ensure we still reap it
			go func(cmd *exec.Cmd) { _, _ = cmd.Process.Wait() }(s.ffmpegCmd)
		}
	}

	// Cancel main stream context
	if s.cancelFunc != nil {
		logger.Debug("Cancelling stream context")
		s.cancelFunc()
	}

	s.isRunning = false
	s.cancelFunc = nil
	s.browserCancels = nil
	s.ffmpegCmd = nil

	logger.Info("Existing stream stopped")
}

// Function to get if the current stream is running
func (s *StreamState) isStreamRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// Function to restart the stream
func RestartStream(ctx context.Context, config *Config) error {
	logger := utils.GetLoggerFromContext(ctx)
	logger.Info("Triggering stream restart...")

	// Stop the current stream - the main loop will automatically restart it
	globalStreamState.stopStream(logger)

	return nil
}

// Function to check if the stream is currently running
func IsStreamRunning() bool {
	return globalStreamState.isStreamRunning()
}

// Function to stop the current stream if it's running
func StopCurrentStream(ctx context.Context) {
	logger := utils.GetLoggerFromContext(ctx)
	globalStreamState.stopStream(logger)
}

// Struct representing the configuration for the stream
type Config struct {
	// The URL of the webstie to stream
	WebpageURL string
	// The RTMP URL to stream to
	RTMPURL string
	// All stream variants to include in the enhanced RTMP output
	Variants []StreamVariant
	// Render targets needed to produce the configured variants
	RenderTargets []RenderTarget
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == RenderTargetsCommand {
		if err := printRenderTargets(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	logger := utils.GetLogger()

	// Create context with logger
	ctx := utils.SaveLoggerToContext(context.Background(), logger)

	// Load configuration with logging available
	config, err := loadConfig(ctx)
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	logger.Debug("Starting webpage stream capture",
		zap.String("webpage", config.WebpageURL),
		zap.String("rtmp", config.RTMPURL),
		zap.Int("variantCount", len(config.Variants)),
		zap.Int("renderTargetCount", len(config.RenderTargets)))

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
	cronScheduler := setupStreamStatusChecker(ctx, config)

	// Handle graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		logger.Info("Received shutdown signal, stopping...")
		signal.Stop(c)
		// Stop current stream if running
		StopCurrentStream(ctx)
		if cronScheduler != nil {
			cronScheduler.Stop()
		}

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
		// Case to handle an expected shutdown signal
		case <-ctx.Done():
			logger.Info("Context cancelled, exiting...")
			return
		// Default case to start or restart the stream
		// This will be triggered by the cron job or manual restarts
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

// Function to setup HTTP routes for metrics and health checks
func setupHTTPRoutes() {
	// Setup prometheus metrics
	http.Handle("/metrics", promhttp.Handler())
	// Setup health endpoint
	http.HandleFunc("/health", getHealthResponse)
}

func printRenderTargets() error {
	logger := zap.NewNop()
	ctx := utils.SaveLoggerToContext(context.Background(), logger)

	config, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	for _, line := range renderTargetLines(config) {
		fmt.Println(line)
	}

	return nil
}

// Function to load configuration from environment variables
func loadConfig(ctx context.Context) (*Config, error) {
	config := &Config{
		WebpageURL: utils.GetEnvOrDefault("WEBPAGE_URL", DefaultWebpageURL),
		RTMPURL:    utils.GetEnvOrDefault("RTMP_URL", DefaultRTMPURL),
	}

	variants, renderTargets, err := loadVariants(ctx)
	if err != nil {
		return nil, err
	}
	config.Variants = variants
	config.RenderTargets = renderTargets

	return config, nil
}

// Function to stream the specified webpage using Chrome and FFmpeg
func streamWebpage(ctx context.Context, config *Config) error {
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

	if err := applyRenderTargetDisplays(config); err != nil {
		return fmt.Errorf("failed to resolve render target displays: %v", err)
	}

	refreshInterval, err := parseRefreshInterval(ctx)
	if err != nil {
		logger.Warn("Invalid WEBPAGE_REFRESH_INTERVAL value, automatic refresh disabled", zap.Error(err))
		refreshInterval = 0
	} else if refreshInterval > 0 {
		logger.Info("Enabling automatic browser refresh", zap.Int("refreshInterval", refreshInterval))
	}

	browserCancels := make([]context.CancelFunc, 0, len(config.RenderTargets)*2)
	defer cancelBrowserSessions(browserCancels)

	for _, renderTarget := range config.RenderTargets {
		target := renderTarget
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", false),
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
			chromedp.Flag("memory-pressure-off", true),
			chromedp.Flag("disable-background-timer-throttling", true),
			chromedp.Flag("disable-renderer-backgrounding", true),
			chromedp.Flag("disable-backgrounding-occluded-windows", true),
			chromedp.Flag("disable-features", "TranslateUI,VizDisplayCompositor"),
			chromedp.Flag("aggressive-cache-discard", true),
			chromedp.WindowSize(target.Width, target.Height),
			chromedp.Env("DISPLAY="+target.Display),
		)

		allocCtx, allocCancel := chromedp.NewExecAllocator(streamCtx, opts...)
		chromeCtx, chromeCancel := chromedp.NewContext(allocCtx)
		browserCancels = append(browserCancels, chromeCancel, allocCancel)

		logger.Info("Starting Chrome browser",
			zap.String("url", config.WebpageURL),
			zap.String("renderTarget", target.Name),
			zap.String("display", target.Display),
			zap.Int("width", target.Width),
			zap.Int("height", target.Height))

		if err := chromedp.Run(chromeCtx,
			chromedp.Navigate(config.WebpageURL),
			chromedp.WaitVisible("body", chromedp.ByQuery),
		); err != nil {
			return fmt.Errorf("failed to navigate to webpage for render target %q: %v", target.Name, err)
		}

		if refreshInterval > 0 {
			go func(renderTargetName string, renderCtx context.Context) {
				ticker := time.NewTicker(time.Duration(refreshInterval) * time.Second)
				defer ticker.Stop()

				for {
					select {
					case <-ticker.C:
						logger.Info("Refreshing browser page", zap.String("url", config.WebpageURL), zap.String("renderTarget", renderTargetName))
						if err := chromedp.Run(renderCtx, chromedp.Reload()); err != nil {
							logger.Error("Failed to refresh browser page", zap.String("renderTarget", renderTargetName), zap.Error(err))
						}
					case <-streamCtx.Done():
						return
					}
				}
			}(target.Name, chromeCtx)
		}
	}

	time.Sleep(3 * time.Second)

	return startFFmpegStream(streamCtx, config, streamCancel, browserCancels)
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

func cancelBrowserSessions(browserCancels []context.CancelFunc) {
	for index := len(browserCancels) - 1; index >= 0; index-- {
		if browserCancels[index] != nil {
			browserCancels[index]()
		}
	}
}

// Function to start FFmpeg stream with the given configuration
func startFFmpegStream(ctx context.Context, config *Config, streamCancel context.CancelFunc, browserCancels []context.CancelFunc) error {
	logger := utils.GetLoggerFromContext(ctx)

	logger.Info("Starting FFmpeg stream")

	args, err := buildFFmpegArgs(config)
	if err != nil {
		return err
	}

	zapWriter := &zapio.Writer{Log: logger, Level: zap.DebugLevel}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stdout = zapWriter
	cmd.Stderr = zapWriter

	logger.Debug("Starting FFmpeg with command", zap.Strings("args", args))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	// Start a goroutine to periodically flush the zapWriter while the command is running
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Check if the process is still running
				if cmd.Process != nil && cmd.ProcessState == nil {
					// Sync forces any buffered output to be written
					zapWriter.Sync()
				} else {
					// Process is done or never started
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Register this stream as running
	globalStreamState.setStreamRunning(streamCancel, browserCancels, cmd)

	logger.Debug("FFmpeg started successfully, streaming...")

	// Wait for the command to finish or context to be cancelled
	err = cmd.Wait()

	// Clean up global stream state when done
	defer func() {
		cancelBrowserSessions(browserCancels)
		globalStreamState.mu.Lock()
		globalStreamState.isRunning = false
		globalStreamState.cancelFunc = nil
		globalStreamState.browserCancels = nil
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
func setupStreamStatusChecker(ctx context.Context, config *Config) *cron.Cron {
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
		return c
	} else {
		logger.Debug("Stream status checker not configured, skipping setup")
	}
	return nil
}
