# Setup base
FROM golang:1.25.4 AS base
    WORKDIR /app
    COPY ./go.mod ./
    COPY ./go.sum ./
    RUN go mod download
    COPY cmd/ ./cmd/
    COPY twitch/ ./twitch/
    COPY utils/ ./utils/

# Setup builder
FROM base AS builder
    RUN go build -o /stream ./cmd/main.go

# Run using Chromium image with FFmpeg support
FROM linuxserver/chromium:latest AS runner
    # Install FFmpeg and runtime dependencies
    RUN apt-get update && apt-get install -y \
        ffmpeg \
        xvfb \
        x11-utils \
        pulseaudio \
        pulseaudio-utils \
        alsa-utils \
        libasound2-plugins \
        dbus \
        && rm -rf /var/lib/apt/lists/*

    # Create directories for audio configs
    RUN mkdir -p /root/.config/pulse /var/run/pulse /var/run/dbus

    # Copy startup script
    COPY start.sh /start.sh
    RUN chmod +x /start.sh

    # Copy app binary
    COPY --from=builder /stream /stream

    # Set environment variables (DISPLAY will be set dynamically in start.sh)
    ENV PULSE_RUNTIME_PATH=/var/run/pulse

    ENTRYPOINT ["/start.sh"]
