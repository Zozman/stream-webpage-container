package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/Zozman/stream-webpage-container/utils"
)

const (
	StreamVariantsEnv    = "STREAM_VARIANTS"
	StreamRenderDisplays = "STREAM_RENDER_DISPLAYS"
	RenderTargetsCommand = "render-targets"
	DefaultAudioBitrate  = "160k"
	DefaultDisplayBase   = 100
	DefaultVideoCodec    = "libx264"
	DefaultVideoPreset   = "veryfast"
	DefaultVideoTune     = "zerolatency"
	DefaultVideoCRF      = "23"
)

var (
	variantNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	bitratePattern     = regexp.MustCompile(`^[1-9][0-9]*k$`)
)

type StreamVariantConfig struct {
	Name         string `json:"name"`
	Resolution   string `json:"resolution,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	Framerate    string `json:"framerate,omitempty"`
	VideoBitrate string `json:"videoBitrate,omitempty"`
	AudioSource  bool   `json:"audioSource,omitempty"`
}

type StreamVariant struct {
	Name             string
	Resolution       string
	Width            int
	Height           int
	Framerate        string
	FramerateInt     int
	VideoBitrate     string
	AudioSource      bool
	RenderTargetName string
}

type RenderTarget struct {
	Name           string
	Width          int
	Height         int
	Framerate      string
	FramerateInt   int
	Display        string
	AudioSource    bool
	VariantNames   []string
	AspectRatioKey string
}

func loadVariants(ctx context.Context, legacyResolution, legacyFramerate string) ([]StreamVariant, []RenderTarget, error) {
	logger := utils.GetLoggerFromContext(ctx)
	rawVariants := strings.TrimSpace(utils.GetEnvOrDefault(StreamVariantsEnv, ""))

	var variants []StreamVariant
	if rawVariants == "" {
		variant, err := buildLegacyVariant(legacyResolution, legacyFramerate)
		if err != nil {
			return nil, nil, err
		}
		variants = []StreamVariant{variant}
	} else {
		var parsed []StreamVariantConfig
		if err := json.Unmarshal([]byte(rawVariants), &parsed); err != nil {
			return nil, nil, fmt.Errorf("failed to parse %s: %w", StreamVariantsEnv, err)
		}

		if len(parsed) == 0 {
			return nil, nil, fmt.Errorf("%s must contain at least one variant", StreamVariantsEnv)
		}

		seenNames := map[string]struct{}{}
		audioSourceCount := 0
		for _, variantConfig := range parsed {
			variant, err := normalizeVariantConfig(variantConfig, legacyFramerate)
			if err != nil {
				return nil, nil, err
			}

			if _, exists := seenNames[variant.Name]; exists {
				return nil, nil, fmt.Errorf("duplicate variant name %q in %s", variant.Name, StreamVariantsEnv)
			}
			seenNames[variant.Name] = struct{}{}

			if variant.AudioSource {
				audioSourceCount++
			}

			variants = append(variants, variant)
		}

		switch {
		case audioSourceCount == 0:
			return nil, nil, fmt.Errorf("%s must mark exactly one variant as audioSource", StreamVariantsEnv)
		case audioSourceCount > 1:
			return nil, nil, fmt.Errorf("%s can only have one variant marked as audioSource", StreamVariantsEnv)
		}
	}

	renderTargets := buildRenderTargets(variants)
	logger.Debug("Resolved stream variants", zap.Int("variantCount", len(variants)), zap.Int("renderTargetCount", len(renderTargets)))

	return variants, renderTargets, nil
}

func buildLegacyVariant(resolution, framerate string) (StreamVariant, error) {
	normalizedResolution, width, height, err := resolveVariantDimensions(resolution, 0, 0)
	if err != nil {
		return StreamVariant{}, err
	}

	normalizedFramerate, framerateInt := normalizeFramerate(framerate)

	return StreamVariant{
		Name:         "default",
		Resolution:   normalizedResolution,
		Width:        width,
		Height:       height,
		Framerate:    normalizedFramerate,
		FramerateInt: framerateInt,
		VideoBitrate: defaultBitrateForDimensions(width, height, framerateInt),
		AudioSource:  true,
	}, nil
}

func normalizeVariantConfig(config StreamVariantConfig, fallbackFramerate string) (StreamVariant, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		return StreamVariant{}, fmt.Errorf("stream variant name is required")
	}
	if !variantNamePattern.MatchString(name) {
		return StreamVariant{}, fmt.Errorf("stream variant name %q must use only letters, numbers, underscores, or hyphens", name)
	}

	resolution, width, height, err := resolveVariantDimensions(config.Resolution, config.Width, config.Height)
	if err != nil {
		return StreamVariant{}, fmt.Errorf("invalid stream variant %q: %w", name, err)
	}

	framerateValue := strings.TrimSpace(config.Framerate)
	if framerateValue == "" {
		framerateValue = fallbackFramerate
	}

	framerate, framerateInt := normalizeFramerate(framerateValue)

	videoBitrate := strings.TrimSpace(config.VideoBitrate)
	if videoBitrate == "" {
		videoBitrate = defaultBitrateForDimensions(width, height, framerateInt)
	} else if !bitratePattern.MatchString(videoBitrate) {
		return StreamVariant{}, fmt.Errorf("stream variant %q has invalid videoBitrate %q", name, videoBitrate)
	}

	return StreamVariant{
		Name:         name,
		Resolution:   resolution,
		Width:        width,
		Height:       height,
		Framerate:    framerate,
		FramerateInt: framerateInt,
		VideoBitrate: videoBitrate,
		AudioSource:  config.AudioSource,
	}, nil
}

func resolveVariantDimensions(resolution string, width, height int) (string, int, int, error) {
	if width > 0 || height > 0 {
		if width <= 0 || height <= 0 {
			return "", 0, 0, fmt.Errorf("width and height must both be positive when either is provided")
		}
		return inferResolutionName(width, height), width, height, nil
	}

	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "720p", "":
		return "720p", 1280, 720, nil
	case "1080p":
		return "1080p", 1920, 1080, nil
	case "2k":
		return "2k", 2560, 1440, nil
	default:
		return "", 0, 0, fmt.Errorf("unsupported resolution %q", resolution)
	}
}

func inferResolutionName(width, height int) string {
	switch {
	case width == 1280 && height == 720:
		return "720p"
	case width == 1920 && height == 1080:
		return "1080p"
	case width == 2560 && height == 1440:
		return "2k"
	default:
		return fmt.Sprintf("%dx%d", width, height)
	}
}

func normalizeFramerate(value string) (string, int) {
	switch strings.TrimSpace(value) {
	case "60":
		return "60", 60
	default:
		return "30", 30
	}
}

func defaultBitrateForDimensions(width, height, framerateInt int) string {
	maxDimension := max(width, height)

	switch {
	case maxDimension >= 2560:
		if framerateInt >= 60 {
			return "8500k"
		}
		return "6000k"
	case maxDimension >= 1920:
		if framerateInt >= 60 {
			return "6000k"
		}
		return "4500k"
	default:
		if framerateInt >= 60 {
			return "4000k"
		}
		return "3000k"
	}
}

func buildRenderTargets(variants []StreamVariant) []RenderTarget {
	renderTargets := make([]RenderTarget, 0, len(variants))
	renderTargetIndexes := map[string]int{}

	for i := range variants {
		aspectKey := aspectRatioKey(variants[i].Width, variants[i].Height)
		targetIndex, exists := renderTargetIndexes[aspectKey]
		if !exists {
			renderTargets = append(renderTargets, RenderTarget{
				Name:           variants[i].Name,
				Width:          variants[i].Width,
				Height:         variants[i].Height,
				Framerate:      variants[i].Framerate,
				FramerateInt:   variants[i].FramerateInt,
				AudioSource:    variants[i].AudioSource,
				VariantNames:   []string{variants[i].Name},
				AspectRatioKey: aspectKey,
			})
			targetIndex = len(renderTargets) - 1
			renderTargetIndexes[aspectKey] = targetIndex
		} else {
			target := &renderTargets[targetIndex]
			target.Width = max(target.Width, variants[i].Width)
			target.Height = max(target.Height, variants[i].Height)
			if variants[i].FramerateInt > target.FramerateInt {
				target.Framerate = variants[i].Framerate
				target.FramerateInt = variants[i].FramerateInt
			}
			target.AudioSource = target.AudioSource || variants[i].AudioSource
			target.VariantNames = append(target.VariantNames, variants[i].Name)
		}

		variants[i].RenderTargetName = renderTargets[targetIndex].Name
	}

	return renderTargets
}

func aspectRatioKey(width, height int) string {
	divisor := gcd(width, height)
	return fmt.Sprintf("%d:%d", width/divisor, height/divisor)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

func renderTargetLines(config *Config) []string {
	lines := make([]string, 0, len(config.RenderTargets))
	for _, target := range config.RenderTargets {
		lines = append(lines, fmt.Sprintf("%s|%d|%d", target.Name, target.Width, target.Height))
	}
	return lines
}

func applyRenderTargetDisplays(config *Config) error {
	displayMap, err := parseRenderTargetDisplayMap(os.Getenv(StreamRenderDisplays))
	if err != nil {
		return err
	}

	if len(displayMap) == 0 {
		defaultDisplay := os.Getenv("DISPLAY")
		if defaultDisplay == "" {
			defaultDisplay = ":0"
		}
		if len(config.RenderTargets) == 1 {
			config.RenderTargets[0].Display = defaultDisplay
			return nil
		}
		return fmt.Errorf("missing %s for %d render targets", StreamRenderDisplays, len(config.RenderTargets))
	}

	for i := range config.RenderTargets {
		display, exists := displayMap[config.RenderTargets[i].Name]
		if !exists {
			return fmt.Errorf("missing display assignment for render target %q", config.RenderTargets[i].Name)
		}
		config.RenderTargets[i].Display = display
	}

	return nil
}

func parseRenderTargetDisplayMap(raw string) (map[string]string, error) {
	displayMap := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return displayMap, nil
	}

	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid %s entry %q", StreamRenderDisplays, entry)
		}

		name := strings.TrimSpace(parts[0])
		display := strings.TrimSpace(parts[1])
		if name == "" || display == "" {
			return nil, fmt.Errorf("invalid %s entry %q", StreamRenderDisplays, entry)
		}

		displayMap[name] = display
	}

	return displayMap, nil
}

func parseRefreshInterval(ctx context.Context) (int, error) {
	logger := utils.GetLoggerFromContext(ctx)

	refreshIntervalStr := utils.GetEnvOrDefault("WEBPAGE_REFRESH_INTERVAL", "")
	if refreshIntervalStr == "" {
		logger.Debug("WEBPAGE_REFRESH_INTERVAL not set, automatic refresh disabled")
		return 0, nil
	}

	refreshInterval, err := strconv.Atoi(refreshIntervalStr)
	if err != nil || refreshInterval <= 0 {
		return 0, fmt.Errorf("invalid WEBPAGE_REFRESH_INTERVAL value %q", refreshIntervalStr)
	}

	return refreshInterval, nil
}

func buildFFmpegArgs(config *Config) ([]string, error) {
	if len(config.RenderTargets) == 0 {
		return nil, fmt.Errorf("no render targets configured")
	}

	variantByName := map[string]StreamVariant{}
	for _, variant := range config.Variants {
		variantByName[variant.Name] = variant
	}

	args := make([]string, 0, len(config.RenderTargets)*8+len(config.Variants)*10)
	for _, target := range config.RenderTargets {
		if target.Display == "" {
			return nil, fmt.Errorf("render target %q does not have a display assignment", target.Name)
		}

		args = append(args,
			"-f", "x11grab",
			"-video_size", fmt.Sprintf("%dx%d", target.Width, target.Height),
			"-framerate", target.Framerate,
			"-draw_mouse", "0",
			"-i", fmt.Sprintf("%s+0,0", target.Display),
		)
	}

	audioTargetIndex := slices.IndexFunc(config.RenderTargets, func(target RenderTarget) bool {
		return target.AudioSource
	})
	if audioTargetIndex == -1 {
		return nil, fmt.Errorf("no audio source render target configured")
	}

	audioInputIndex := len(config.RenderTargets)
	args = append(args, "-f", "alsa", "-i", "default")

	filterParts := make([]string, 0, len(config.Variants)*2)
	outputLabels := make([]string, 0, len(config.Variants))

	for targetIndex, target := range config.RenderTargets {
		variantNames := target.VariantNames
		sourceLabels := make([]string, 0, len(variantNames))

		if len(variantNames) > 1 {
			splitOutputs := make([]string, 0, len(variantNames))
			for variantIndex := range variantNames {
				splitOutputs = append(splitOutputs, fmt.Sprintf("[target%dsrc%d]", targetIndex, variantIndex))
			}
			filterParts = append(filterParts, fmt.Sprintf("[%d:v]split=%d%s", targetIndex, len(variantNames), strings.Join(splitOutputs, "")))
			sourceLabels = append(sourceLabels, splitOutputs...)
		} else {
			sourceLabels = append(sourceLabels, fmt.Sprintf("[%d:v]", targetIndex))
		}

		for variantIndex, variantName := range variantNames {
			variant := variantByName[variantName]
			outputLabel := fmt.Sprintf("[variant_%d]", len(outputLabels))
			filterParts = append(filterParts, fmt.Sprintf("%sfps=%s,scale=%d:%d:flags=lanczos,setsar=1%s", sourceLabels[variantIndex], variant.Framerate, variant.Width, variant.Height, outputLabel))
			outputLabels = append(outputLabels, outputLabel)
		}
	}

	args = append(args, "-filter_complex", strings.Join(filterParts, ";"))
	for _, outputLabel := range outputLabels {
		args = append(args, "-map", outputLabel)
	}
	args = append(args, "-map", fmt.Sprintf("%d:a", audioInputIndex))
	args = append(args,
		"-c:v", DefaultVideoCodec,
		"-preset", DefaultVideoPreset,
		"-tune", DefaultVideoTune,
		"-crf", DefaultVideoCRF,
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", DefaultAudioBitrate,
		"-ar", "44100",
		"-ac", "2",
	)

	for variantIndex, variant := range config.Variants {
		bufferSize := fmt.Sprintf("%dk", extractNumberFromBitrate(variant.VideoBitrate)*2)
		keyframeInterval := fmt.Sprintf("%d", variant.FramerateInt*2)
		args = append(args,
			fmt.Sprintf("-b:v:%d", variantIndex), variant.VideoBitrate,
			fmt.Sprintf("-maxrate:v:%d", variantIndex), variant.VideoBitrate,
			fmt.Sprintf("-bufsize:v:%d", variantIndex), bufferSize,
			fmt.Sprintf("-g:v:%d", variantIndex), keyframeInterval,
			fmt.Sprintf("-metadata:s:v:%d", variantIndex), fmt.Sprintf("variant_name=%s", variant.Name),
		)
	}

	args = append(args, "-f", "flv", config.RTMPURL)

	return args, nil
}
