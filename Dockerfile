# Setup base
FROM golang:1.24.5 AS base
WORKDIR /app
COPY ./go.mod ./
COPY ./go.sum ./
# Use direct proxy to avoid TLS issues
ENV GOPROXY=direct
RUN go mod download
COPY ./main.go ./

# Setup builder
FROM base AS builder
RUN go build -o /stream ./main.go

# Run using FFmpeg image with Chromium support
FROM linuxserver/ffmpeg:7.1.1 AS runner

# Install runtime dependencies and Chromium
RUN apt-get update && apt-get install -y \
    xvfb \
    x11-utils \
    pulseaudio \
    pulseaudio-utils \
    alsa-utils \
    libasound2-plugins \
    dbus \
    chromium-browser \
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

# Expose FFmpeg debug info
ENV FFREPORT=file=/tmp/ffmpeg-%p-%t.log:level=32

ENTRYPOINT ["/start.sh"]
