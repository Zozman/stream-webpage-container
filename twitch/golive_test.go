package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zozman/stream-webpage-container/utils"
	"go.uber.org/zap"
)

func testContext() context.Context {
	logger, _ := zap.NewDevelopment()
	return utils.SaveLoggerToContext(context.Background(), logger)
}

func TestCallGoLiveAPI_Success(t *testing.T) {
	resp := GoLiveResponse{
		Meta: GoLiveMeta{
			ConfigID:      "test-config-id-123",
			Service:       "IVS",
			SchemaVersion: "2023-07-29",
		},
		Status: GoLiveStatus{
			Result: "success",
		},
		IngestEndpoints: []GoLiveIngestEndpoint{
			{
				Protocol:       "RTMP",
				URLTemplate:    "rtmp://sea02.contribute.live-video.net/app/{stream_key}",
				Authentication: "v1_special_auth_token_live_123",
			},
			{
				Protocol:       "RTMPS",
				URLTemplate:    "rtmps://sea02.contribute.live-video.net/app/{stream_key}",
				Authentication: "v1_special_auth_token_live_123",
			},
		},
		EncoderConfigurations: []GoLiveEncoderConfiguration{
			{
				Type:   "obs_x264",
				Width:  1920,
				Height: 1080,
				Framerate: &GoLiveFramerate{Numerator: 60, Denominator: 1},
				Settings: GoLiveEncoderSettings{
					Bitrate:     6000,
					RateControl: "CBR",
					KeyintSec:   2,
					Profile:     "high",
				},
			},
			{
				Type:   "obs_x264",
				Width:  1280,
				Height: 720,
				Framerate: &GoLiveFramerate{Numerator: 60, Denominator: 1},
				Settings: GoLiveEncoderSettings{
					Bitrate:     3000,
					RateControl: "CBR",
					KeyintSec:   2,
					Profile:     "high",
				},
			},
			{
				Type:   "obs_x264",
				Width:  852,
				Height: 480,
				Framerate: &GoLiveFramerate{Numerator: 30, Denominator: 1},
				Settings: GoLiveEncoderSettings{
					Bitrate:     1500,
					RateControl: "CBR",
					KeyintSec:   2,
					Profile:     "main",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		var req GoLiveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}

		if req.Service != "IVS" {
			t.Errorf("expected service IVS, got %s", req.Service)
		}
		if req.Authentication != "live_test_key_123" {
			t.Errorf("expected authentication live_test_key_123, got %s", req.Authentication)
		}
		if req.Client.Name != "stream-webpage-container" {
			t.Errorf("expected client name stream-webpage-container, got %s", req.Client.Name)
		}
		if len(req.Client.SupportedCodecs) != 1 || req.Client.SupportedCodecs[0] != "h264" {
			t.Errorf("expected supported_codecs [h264], got %v", req.Client.SupportedCodecs)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Override the URL for testing by using a custom HTTP call
	ctx := testContext()
	result, err := callGoLiveAPIWithURL(ctx, "live_test_key_123", GoLiveOptions{}, server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Meta.ConfigID != "test-config-id-123" {
		t.Errorf("expected config_id test-config-id-123, got %s", result.Meta.ConfigID)
	}
	if len(result.EncoderConfigurations) != 3 {
		t.Fatalf("expected 3 encoder configurations, got %d", len(result.EncoderConfigurations))
	}
	if result.EncoderConfigurations[0].Width != 1920 || result.EncoderConfigurations[0].Height != 1080 {
		t.Errorf("expected first encoder 1920x1080, got %dx%d",
			result.EncoderConfigurations[0].Width, result.EncoderConfigurations[0].Height)
	}
	if result.EncoderConfigurations[0].Settings.Bitrate != 6000 {
		t.Errorf("expected first encoder bitrate 6000, got %d", result.EncoderConfigurations[0].Settings.Bitrate)
	}
	if len(result.IngestEndpoints) != 2 {
		t.Errorf("expected 2 ingest endpoints, got %d", len(result.IngestEndpoints))
	}
	if result.IngestEndpoints[0].Authentication != "v1_special_auth_token_live_123" {
		t.Errorf("unexpected authentication token: %s", result.IngestEndpoints[0].Authentication)
	}
}

func TestCallGoLiveAPI_ErrorStatus(t *testing.T) {
	resp := GoLiveResponse{
		Status: GoLiveStatus{
			Result:   "error",
			HTMLEnUS: "Your GPU is not supported for Enhanced Broadcasting.",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := testContext()
	_, err := callGoLiveAPIWithURL(ctx, "live_test_key", GoLiveOptions{}, server.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "Go Live API returned error: Your GPU is not supported for Enhanced Broadcasting." {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestCallGoLiveAPI_WarningStatus(t *testing.T) {
	resp := GoLiveResponse{
		Meta: GoLiveMeta{ConfigID: "warn-config"},
		Status: GoLiveStatus{
			Result:   "warning",
			HTMLEnUS: "Your driver version will be deprecated soon.",
		},
		IngestEndpoints: []GoLiveIngestEndpoint{
			{Protocol: "RTMP", URLTemplate: "rtmp://test/app/{stream_key}", Authentication: "auth_key"},
		},
		EncoderConfigurations: []GoLiveEncoderConfiguration{
			{Width: 1920, Height: 1080, Settings: GoLiveEncoderSettings{Bitrate: 6000}},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := testContext()
	result, err := callGoLiveAPIWithURL(ctx, "live_test_key", GoLiveOptions{}, server.URL)
	if err != nil {
		t.Fatalf("warning should not return error, got: %v", err)
	}
	if result.Status.Result != "warning" {
		t.Errorf("expected warning status, got %s", result.Status.Result)
	}
	if result.Meta.ConfigID != "warn-config" {
		t.Errorf("expected config_id warn-config, got %s", result.Meta.ConfigID)
	}
}

func TestCallGoLiveAPI_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	ctx := testContext()
	_, err := callGoLiveAPIWithURL(ctx, "live_test_key", GoLiveOptions{}, server.URL)
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
}

func TestCallGoLiveAPI_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	ctx := testContext()
	_, err := callGoLiveAPIWithURL(ctx, "live_test_key", GoLiveOptions{}, server.URL)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
