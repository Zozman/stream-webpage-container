package main

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Zozman/stream-webpage-container/utils"
)

func TestLoadConfigWithStreamVariants(t *testing.T) {
	t.Setenv("WEBPAGE_URL", "https://example.com")
	t.Setenv("RTMP_URL", "rtmp://example.com/live/stream")
	t.Setenv(StreamVariantsEnv, `[{"name":"landscape","width":1920,"height":1080,"framerate":"60","videoBitrate":"6000k","audioSource":true},{"name":"landscape720","width":1280,"height":720,"framerate":"30","videoBitrate":"3000k"},{"name":"portrait","width":1080,"height":1920,"framerate":"60","videoBitrate":"4500k"}]`)

	logger := zap.NewNop()
	ctx := utils.SaveLoggerToContext(context.Background(), logger)

	config, err := loadConfig(ctx)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(config.Variants) != 3 {
		t.Fatalf("Expected 3 variants, got %d", len(config.Variants))
	}
	if len(config.RenderTargets) != 2 {
		t.Fatalf("Expected 2 render targets, got %d", len(config.RenderTargets))
	}

	if config.RenderTargets[0].Name != "landscape" {
		t.Fatalf("Expected first render target to be landscape, got %q", config.RenderTargets[0].Name)
	}
	if got := config.RenderTargets[0].VariantNames; len(got) != 2 || got[0] != "landscape" || got[1] != "landscape720" {
		t.Fatalf("Unexpected landscape variant mapping: %#v", got)
	}
	if !config.RenderTargets[0].AudioSource {
		t.Fatal("Expected landscape render target to be audio source")
	}
	if config.RenderTargets[1].Name != "portrait" {
		t.Fatalf("Expected second render target to be portrait, got %q", config.RenderTargets[1].Name)
	}
}

func TestLoadConfigRejectsMissingAudioSource(t *testing.T) {
	t.Setenv(StreamVariantsEnv, `[{"name":"landscape","width":1920,"height":1080}]`)

	logger := zap.NewNop()
	ctx := utils.SaveLoggerToContext(context.Background(), logger)

	_, err := loadConfig(ctx)
	if err == nil || !strings.Contains(err.Error(), "audioSource") {
		t.Fatalf("Expected audioSource validation error, got %v", err)
	}
}

func TestBuildFFmpegArgs(t *testing.T) {
	config := &Config{
		WebpageURL: "https://example.com",
		RTMPURL:    "rtmp://example.com/live/stream",
		Variants: []StreamVariant{
			{
				Name:             "landscape",
				Width:            1920,
				Height:           1080,
				Framerate:        "60",
				FramerateInt:     60,
				VideoBitrate:     "6000k",
				AudioSource:      true,
				RenderTargetName: "landscape",
			},
			{
				Name:             "landscape720",
				Width:            1280,
				Height:           720,
				Framerate:        "30",
				FramerateInt:     30,
				VideoBitrate:     "3000k",
				RenderTargetName: "landscape",
			},
			{
				Name:             "portrait",
				Width:            1080,
				Height:           1920,
				Framerate:        "60",
				FramerateInt:     60,
				VideoBitrate:     "4500k",
				RenderTargetName: "portrait",
			},
		},
		RenderTargets: []RenderTarget{
			{
				Name:         "landscape",
				Width:        1920,
				Height:       1080,
				Framerate:    "60",
				FramerateInt: 60,
				Display:      ":100",
				AudioSource:  true,
				VariantNames: []string{"landscape", "landscape720"},
			},
			{
				Name:         "portrait",
				Width:        1080,
				Height:       1920,
				Framerate:    "60",
				FramerateInt: 60,
				Display:      ":101",
				VariantNames: []string{"portrait"},
			},
		},
	}

	args, err := buildFFmpegArgs(config)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	command := strings.Join(args, " ")
	expectedSnippets := []string{
		"-i :100+0,0",
		"-i :101+0,0",
		"-filter_complex [0:v]split=2[target0src0][target0src1];[target0src0]fps=60,scale=1920:1080:flags=lanczos,setsar=1[variant_0];[target0src1]fps=30,scale=1280:720:flags=lanczos,setsar=1[variant_1];[1:v]fps=60,scale=1080:1920:flags=lanczos,setsar=1[variant_2]",
		"-map [variant_0] -map [variant_1] -map [variant_2] -map 2:a",
		"-metadata:s:v:0 variant_name=landscape",
		"-metadata:s:v:1 variant_name=landscape720",
		"-metadata:s:v:2 variant_name=portrait",
		"-f flv rtmp://example.com/live/stream",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(command, snippet) {
			t.Fatalf("Expected command to contain %q, got %q", snippet, command)
		}
	}
}
