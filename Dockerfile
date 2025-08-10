# Setup base
FROM golang:1.24.5 AS base
WORKDIR /app
COPY ./go.mod ./
COPY ./go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY utils/ ./utils/

# Setup builder
FROM base AS builder
RUN go build -o /stream ./cmd/main.go

# Download Chrome
FROM curlimages/curl:latest AS downloader
RUN curl -fsSL https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb \
    -o google-chrome-stable_current_amd64.deb

# Run using FFmpeg image with Chrome support
FROM linuxserver/ffmpeg:7.1.1 AS runner

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    xvfb \
    x11-utils \
    pulseaudio \
    pulseaudio-utils \
    alsa-utils \
    libasound2-plugins \
    dbus \
    && rm -rf /var/lib/apt/lists/*

# Install Chrome from downloaded .deb
COPY --from=downloader /home/curl_user/google-chrome-stable_current_amd64.deb /tmp/google-chrome-stable_current_amd64.deb
RUN apt-get update \
    && apt-get install -y /tmp/google-chrome-stable_current_amd64.deb \
    && rm /tmp/google-chrome-stable_current_amd64.deb \
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
