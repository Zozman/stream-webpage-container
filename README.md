# Stream Website

A Go application that captures live website content (both visual and audio) and streams it to an RTMP endpoint using Chrome headless browser and FFmpeg.

## Features

- **Website Capture**: Captures any website content including dynamic content, videos, and audio
- **Live Streaming**: Streams captured content to RTMP endpoints in real-time
- **Configurable Resolution**: Supports 720p and 1080p output resolutions
- **Docker Support**: Fully containerized with multi-stage builds
- **Graceful Shutdown**: Handles shutdown signals properly
- **Logging**: Structured logging with Zap

## How It Works

1. **Chrome Browser**: Launches a Chrome browser instance to render the target website
2. **Screen Capture**: Uses FFmpeg's x11grab to capture the browser window
3. **Audio Capture**: Captures system audio using PulseAudio
4. **Encoding**: Encodes video with H.264 and audio with AAC
5. **Streaming**: Streams the encoded content to the specified RTMP endpoint

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `WEBSITE_URL` | URL of the website to capture | `https://example.com` |
| `RTMP_URL` | RTMP endpoint to stream to | `rtmp://localhost:1935/live/stream` |
| `RESOLUTION` | Output resolution (720p or 1080p) | `1080p` |

## Quick Start

### Using Docker Compose

1. Clone the repository and navigate to the stream-website directory:
```bash
cd stream-website
```

2. Set environment variables (optional):
```bash
export WEBSITE_URL="https://www.youtube.com/watch?v=dQw4w9WgXcQ"
export RTMP_URL="rtmp://your-rtmp-server/live/stream"
export RESOLUTION="1080p"
```

3. Start the application:
```bash
docker-compose up --build
```

### With RTMP Test Server

To test with a local RTMP server:

```bash
# Start with the included RTMP server
docker-compose --profile rtmp-server up --build

# The RTMP server will be available at:
# - RTMP: rtmp://localhost:1935/live/stream
# - Web interface: http://localhost:8080
# - Stream stats: http://localhost:8080/stat
```

### Using Docker Only

```bash
# Build the image
docker build -t stream-website .

# Run the container
docker run -e WEBSITE_URL="https://example.com" \
           -e RTMP_URL="rtmp://your-server/live/stream" \
           -e RESOLUTION="1080p" \
           --security-opt seccomp:unconfined \
           --cap-add SYS_ADMIN \
           -v /dev/shm:/dev/shm \
           stream-website
```

## Local Development

### Prerequisites

- Go 1.21 or later
- FFmpeg
- Google Chrome
- PulseAudio
- X11 (for display)

### Running Locally

1. Install dependencies:
```bash
go mod download
```

2. Set environment variables:
```bash
export WEBSITE_URL="https://example.com"
export RTMP_URL="rtmp://localhost:1935/live/stream"
export RESOLUTION="1080p"
```

3. Run the application:
```bash
go run main.go
```

## Configuration

### Supported Resolutions

- **720p**: 1280x720 pixels
- **1080p**: 1920x1080 pixels (default)

### FFmpeg Settings

The application uses optimized FFmpeg settings for live streaming:

- **Video Codec**: H.264 with libx264
- **Preset**: veryfast (for low latency)
- **CRF**: 23 (balanced quality)
- **Max Bitrate**: 3000k
- **Buffer Size**: 6000k
- **Audio Codec**: AAC at 128k bitrate
- **Frame Rate**: 30 FPS

## Troubleshooting

### Common Issues

1. **Chrome fails to start**:
   - Ensure the container has proper permissions
   - Check that `--security-opt seccomp:unconfined` and `--cap-add SYS_ADMIN` are set

2. **No audio capture**:
   - Audio is captured using PulseAudio with virtual audio devices
   - The container automatically creates virtual speakers and microphones
   - Check the startup logs for audio device debugging information
   - If audio issues persist, try restarting the container

3. **FFmpeg errors**:
   - Verify the RTMP endpoint is accessible
   - Check network connectivity
   - Ensure the RTMP server supports the streaming format

4. **High CPU usage**:
   - Consider using 720p resolution for lower resource usage
   - Adjust FFmpeg preset (use "faster" or "fast" instead of "veryfast")

### Logs

The application provides structured JSON logs showing:
- Startup configuration
- Browser navigation status
- FFmpeg command execution
- Error details

### Performance Tuning

For better performance:

1. **Use 720p resolution** for lower CPU usage:
```bash
export RESOLUTION="720p"
```

2. **Adjust FFmpeg settings** by modifying the FFmpeg arguments in `main.go`

3. **Allocate more shared memory** by increasing the `/dev/shm` volume size

## Architecture

```
┌─────────────┐    ┌──────────────┐    ┌─────────────┐    ┌──────────────┐
│   Website   │ -> │    Chrome    │ -> │   FFmpeg    │ -> │ RTMP Server  │
│             │    │   Browser    │    │  (capture   │    │              │
│             │    │              │    │ & encode)   │    │              │
└─────────────┘    └──────────────┘    └─────────────┘    └──────────────┘
```

1. Chrome renders the target website
2. FFmpeg captures the Chrome window using x11grab
3. FFmpeg captures system audio using PulseAudio
4. FFmpeg encodes and streams to RTMP endpoint

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test with Docker
5. Submit a pull request

## License

This project is part of the Stream Website ecosystem.
