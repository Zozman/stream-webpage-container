package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Zozman/stream-webpage-container/utils"
	"go.uber.org/zap"
)

const (
	TwitchGoLiveURL = "https://ingest.twitch.tv/api/v3/GetClientConfiguration"
	// Schema "2024-06-04" is the current working version. "2025-01-25" requires a
	// different struct layout (strings for system.build/revision, canvases array)
	// and was returning HTTP 400 with empty body. "2023-05-10" is explicitly deprecated.
	GoLiveSchemaVersion = "2024-06-04"
	GoLiveService       = "IVS"
	DefaultClientName   = "stream-webpage-container"
	GoLiveTimeout       = 10 * time.Second
)

// GoLiveRequest is the POST body sent to Twitch's GetClientConfiguration endpoint.
type GoLiveRequest struct {
	Service       string            `json:"service"`
	SchemaVersion string            `json:"schema_version"`
	Authentication string           `json:"authentication"`
	Client        GoLiveClient      `json:"client"`
	Capabilities  GoLiveCapabilities `json:"capabilities"`
	Preferences   GoLivePreferences  `json:"preferences"`
}

type GoLiveClient struct {
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	SupportedCodecs []string `json:"supported_codecs"`
	VodTrackAudio   bool     `json:"vod_track_audio"`
}

type GoLiveCapabilities struct {
	CPU            GoLiveCPU    `json:"cpu"`
	Memory         GoLiveMemory `json:"memory"`
	GPU            []GoLiveGPU  `json:"gpu"`
	System         GoLiveSystem `json:"system"`
	GamingFeatures *interface{} `json:"gaming_features"`
}

type GoLiveCPU struct {
	PhysicalCores int    `json:"physical_cores"`
	LogicalCores  int    `json:"logical_cores"`
	Speed         int    `json:"speed"`
	Name          string `json:"name"`
}

type GoLiveMemory struct {
	Total int64 `json:"total"`
	Free  int64 `json:"free"`
}

type GoLiveGPU struct {
	Model                string `json:"model"`
	VendorID             int    `json:"vendor_id"`
	DeviceID             int    `json:"device_id"`
	DedicatedVideoMemory int64  `json:"dedicated_video_memory"`
	SharedSystemMemory   int64  `json:"shared_system_memory"`
	DriverVersion        string `json:"driver_version"`
}

// GoLiveSystem describes the OS. Build and Revision are integers in schema "2024-06-04"
// but strings in "2025-01-25" — using the wrong type causes silent HTTP 400 rejections.
type GoLiveSystem struct {
	Version      string `json:"version"`
	Name         string `json:"name"`
	Build        int    `json:"build"`
	Release      string `json:"release"`
	Revision     int    `json:"revision"`
	Bits         int    `json:"bits"`
	ARM          bool   `json:"arm"`
	ARMEmulation bool   `json:"armEmulation"`
}

type GoLivePreferences struct {
	MaximumAggregateBitrate *int64          `json:"maximum_aggregate_bitrate"`
	MaximumVideoTracks      *int            `json:"maximum_video_tracks"`
	CompositionGPUIndex     int             `json:"composition_gpu_index"`
	Width                   int             `json:"width"`
	Height                  int             `json:"height"`
	CanvasWidth             int             `json:"canvas_width"`
	CanvasHeight            int             `json:"canvas_height"`
	Framerate               GoLiveFramerate `json:"framerate"`
	// Canvases is only populated when portrait mode is enabled. The API treats
	// entries here as additional canvases beyond the primary (top-level) one.
	// Each additional canvas must have height > width (portrait orientation).
	Canvases               []GoLiveCanvas  `json:"canvases,omitempty"`
	AudioSamplesPerSec      int            `json:"audio_samples_per_sec"`
	AudioChannels           int            `json:"audio_channels"`
}

type GoLiveCanvas struct {
	Width        int             `json:"width"`
	Height       int             `json:"height"`
	CanvasWidth  int             `json:"canvas_width"`
	CanvasHeight int             `json:"canvas_height"`
	Framerate    GoLiveFramerate `json:"framerate"`
}

type GoLiveFramerate struct {
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
}

// GoLiveResponse is the parsed response from GetClientConfiguration.
type GoLiveResponse struct {
	Meta                   GoLiveMeta                   `json:"meta"`
	Status                 GoLiveStatus                 `json:"status"`
	IngestEndpoints        []GoLiveIngestEndpoint       `json:"ingest_endpoints"`
	EncoderConfigurations  []GoLiveEncoderConfiguration `json:"encoder_configurations"`
}

type GoLiveMeta struct {
	ConfigID      string `json:"config_id"`
	Service       string `json:"service"`
	SchemaVersion string `json:"schema_version"`
}

type GoLiveStatus struct {
	Result  string `json:"result"`
	HTMLEnUS string `json:"html_en_us"`
}

type GoLiveIngestEndpoint struct {
	Protocol       string `json:"protocol"`
	URLTemplate    string `json:"url_template"`
	Authentication string `json:"authentication"`
}

type GoLiveEncoderConfiguration struct {
	Type      string                       `json:"type"`
	Width     int                          `json:"width"`
	Height    int                          `json:"height"`
	Framerate *GoLiveFramerate             `json:"framerate,omitempty"`
	Settings  GoLiveEncoderSettings        `json:"settings"`
}

type GoLiveEncoderSettings struct {
	Bitrate    int    `json:"bitrate"`
	RateControl string `json:"rate_control"`
	KeyintSec  int    `json:"keyint_sec"`
	Profile    string `json:"profile"`
}

// GoLiveOptions configures the Go Live API request. Derived from STREAM_OUTPUTS
// and TWITCH_* env vars so the API request reflects the user's desired setup.
type GoLiveOptions struct {
	// ClientName overrides the client.name field (default: "stream-webpage-container")
	ClientName string
	// MaxTracks caps the number of encoder configurations returned (nil = no limit)
	MaxTracks *int
	// Primary canvas dimensions (from the largest landscape STREAM_OUTPUTS entry)
	CanvasWidth  int
	CanvasHeight int
	Framerate    int
	// PortraitCanvas, if set, adds a second portrait canvas to the request.
	// Derived from the first portrait entry in STREAM_OUTPUTS.
	PortraitCanvas *GoLiveCanvas
}

// CallGoLiveAPI calls Twitch's GetClientConfiguration endpoint to obtain
// multitrack streaming configuration and ingest credentials.
func CallGoLiveAPI(ctx context.Context, streamKey string, opts GoLiveOptions) (*GoLiveResponse, error) {
	return callGoLiveAPIWithURL(ctx, streamKey, opts, TwitchGoLiveURL)
}

func callGoLiveAPIWithURL(ctx context.Context, streamKey string, opts GoLiveOptions, apiURL string) (*GoLiveResponse, error) {
	logger := utils.GetLoggerFromContext(ctx)

	clientName := opts.ClientName
	if clientName == "" {
		clientName = DefaultClientName
	}

	canvasWidth := opts.CanvasWidth
	if canvasWidth == 0 {
		canvasWidth = 1920
	}
	canvasHeight := opts.CanvasHeight
	if canvasHeight == 0 {
		canvasHeight = 1080
	}
	framerate := opts.Framerate
	if framerate == 0 {
		framerate = 60
	}

	// Twitch's Go Live API validates hardware capabilities and rejects requests
	// that don't report a recognized GPU. The API uses this to decide which encoder
	// configurations to return. We report plausible NVIDIA hardware because:
	//   1. The API returns HTTP 200 with "GPU not supported" if vendor_id is 0
	//   2. We only use the response for track resolutions/bitrates — actual encoding
	//      is done with libx264 regardless of what the API recommends
	//   3. The returned encoder_configurations specify OBS-specific encoder types
	//      (e.g. "jim_nvenc") which we ignore; we only extract width/height/bitrate
	req := GoLiveRequest{
		Service:        GoLiveService,
		SchemaVersion:  GoLiveSchemaVersion,
		Authentication: streamKey,
		Client: GoLiveClient{
			Name:            clientName,
			Version:         "2.0.0",
			SupportedCodecs: []string{"h264"},
			VodTrackAudio:   false,
		},
		Capabilities: GoLiveCapabilities{
			// CPU/Memory values are plausible defaults; the API uses them to gauge
			// encoding capacity but we don't rely on its encoder choice.
			CPU: GoLiveCPU{
				PhysicalCores: 8,
				LogicalCores:  8,
				Speed:         3800,
				Name:          "AMD Ryzen 7 5800X",
			},
			Memory: GoLiveMemory{
				Total: 34359738368,
				Free:  16589934592,
			},
			// Must report a recognized GPU vendor (NVIDIA=4318, AMD=4098) or the API
			// rejects with "Your GPU is not currently supported by Enhanced Broadcasting."
			GPU: []GoLiveGPU{
				{
					Model:                "NVIDIA GeForce RTX 4080",
					VendorID:             4318, // 0x10DE (NVIDIA)
					DeviceID:             10114,
					DedicatedVideoMemory: 16106127360,
					SharedSystemMemory:   17079595008,
					DriverVersion:        "566.36",
				},
			},
			System: GoLiveSystem{
				Version:      "6.6",
				Name:         "Linux",
				Build:        0,
				Release:      "6.6",
				Revision:     0,
				Bits:         64,
				ARM:          false,
				ARMEmulation: false,
			},
			GamingFeatures: nil,
		},
		// Canvas dimensions must be flat fields in preferences (not a nested array)
		// for schema "2024-06-04". The API uses these to determine output track layouts.
		// MaximumVideoTracks caps how many encoder configs the API returns — each track
		// requires its own Xvfb + Chrome + FFmpeg encode pipeline, so limit to what
		// the container can sustain.
		Preferences: GoLivePreferences{
			MaximumAggregateBitrate: nil,
			MaximumVideoTracks:      opts.MaxTracks,
			CompositionGPUIndex:     0,
			Width:                   canvasWidth,
			Height:                  canvasHeight,
			CanvasWidth:             canvasWidth,
			CanvasHeight:            canvasHeight,
			Framerate:               GoLiveFramerate{Numerator: framerate, Denominator: 1},
			AudioSamplesPerSec:      44100,
			AudioChannels:           2,
		},
	}

	// When a portrait canvas is provided (derived from a portrait entry in
	// STREAM_OUTPUTS), add it as a second canvas. The API requires all canvases
	// share the same framerate, and each additional canvas must be portrait
	// (height > width). Minimum 2 tracks per canvas.
	if opts.PortraitCanvas != nil {
		req.Preferences.Canvases = []GoLiveCanvas{*opts.PortraitCanvas}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Go Live API request: %w", err)
	}

	logger.Debug("Calling Twitch Go Live API",
		zap.String("url", apiURL),
		zap.String("clientName", clientName),
		zap.String("requestBody", string(body)))

	httpCtx, cancel := context.WithTimeout(ctx, GoLiveTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(httpCtx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create Go Live API request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Go Live API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Go Live API response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Debug("Go Live API error response",
			zap.Int("statusCode", resp.StatusCode),
			zap.String("body", string(respBody)))
		return nil, fmt.Errorf("Go Live API returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var goLiveResp GoLiveResponse
	if err := json.Unmarshal(respBody, &goLiveResp); err != nil {
		return nil, fmt.Errorf("failed to parse Go Live API response: %w", err)
	}

	logger.Debug("Go Live API response received",
		zap.String("status", goLiveResp.Status.Result),
		zap.String("configId", goLiveResp.Meta.ConfigID),
		zap.Int("numEncoders", len(goLiveResp.EncoderConfigurations)),
		zap.Int("numEndpoints", len(goLiveResp.IngestEndpoints)))

	if goLiveResp.Status.Result == "error" {
		msg := goLiveResp.Status.HTMLEnUS
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("Go Live API returned error: %s", msg)
	}

	return &goLiveResp, nil
}
