# Setup base
FROM golang:1.26.4 AS base
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

# Build FFmpeg 8.1.2 from source with x11grab + libx264 + Enhanced FLV multitrack
FROM debian:bookworm-slim AS ffmpeg-build
    ARG FFMPEG_VERSION=8.1.2
    RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        nasm \
        pkg-config \
        libxcb1-dev \
        libxcb-shm0-dev \
        libxcb-xfixes0-dev \
        libxcb-shape0-dev \
        libx264-dev \
        libasound2-dev \
        && rm -rf /var/lib/apt/lists/*
    RUN curl -fsSL "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz" | tar -xJ
    WORKDIR /ffmpeg-${FFMPEG_VERSION}
    RUN ./configure \
        --enable-gpl \
        --enable-libx264 \
        --enable-libxcb \
        --enable-nonfree \
        --disable-doc \
        --disable-ffplay \
        --disable-debug \
        --disable-static \
        --enable-shared \
        && make -j"$(nproc)" \
        && make install

# Run using Chromium image with FFmpeg support
FROM linuxserver/chromium:latest AS runner
    # Install runtime dependencies (Xvfb is managed by the Go app per-output)
    RUN apt-get update && apt-get install -y --no-install-recommends \
        xvfb \
        x11-utils \
        pulseaudio \
        pulseaudio-utils \
        alsa-utils \
        libasound2-plugins \
        dbus \
        libxcb1 \
        libxcb-shm0 \
        libxcb-xfixes0 \
        libxcb-shape0 \
        libx264-dev \
        && rm -rf /var/lib/apt/lists/*

    # Copy FFmpeg shared libraries and binaries built from source
    COPY --from=ffmpeg-build /usr/local/lib/ /usr/local/lib/
    COPY --from=ffmpeg-build /usr/local/bin/ffmpeg /usr/local/bin/ffmpeg
    COPY --from=ffmpeg-build /usr/local/bin/ffprobe /usr/local/bin/ffprobe
    RUN ldconfig

    # Verify FFmpeg version is 8.0+ and x11grab is available
    RUN ffmpeg -version | grep -Eq 'version (8|9|[1-9][0-9])\.' \
        && ffprobe -version >/dev/null \
        && ffmpeg -devices 2>&1 | grep -q x11grab

    # Create directories for audio configs
    RUN mkdir -p /root/.config/pulse /var/run/pulse /var/run/dbus

    # Copy startup script
    COPY start.sh /start.sh
    RUN chmod +x /start.sh

    # Copy app binary
    COPY --from=builder /stream /stream

    # Set environment variables
    ENV PULSE_RUNTIME_PATH=/var/run/pulse

    ENTRYPOINT ["/start.sh"]
