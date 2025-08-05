# Run using FFmpeg image with Chromium support (use Ubuntu 20.04 base)
FROM ubuntu:20.04 AS runner

# Install runtime dependencies, FFmpeg, and Chromium from Ubuntu 20.04 repos
RUN DEBIAN_FRONTEND=noninteractive apt-get update && apt-get install -y \
    ffmpeg \
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

# Copy pre-built app binary
COPY stream /stream

# Set environment variables (DISPLAY will be set dynamically in start.sh)
ENV PULSE_RUNTIME_PATH=/var/run/pulse

# Expose FFmpeg debug info
ENV FFREPORT=file=/tmp/ffmpeg-%p-%t.log:level=32

ENTRYPOINT ["/start.sh"]
