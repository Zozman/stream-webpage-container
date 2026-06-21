# Stream Webpage Container

A containerized application to stream a webpage live over RTMP.  Just pass a `WEBPAGE_URL` and a `RTMP_URL` and the container will open a browser, capture the video and audio, and send it to the specified location.  It can even be configured to automatically restart the stream for supported services when the service stops the stream.

## 2.0: Enhanced RTMP Multitrack Streaming

Version 2.0 adds support for streaming **multiple video tracks** simultaneously over a single Enhanced RTMP connection using [FFmpeg 8.0+'s Enhanced FLV v2](https://github.com/FFmpeg/FFmpeg/blob/release/8.0/Changelog) multitrack support.  This enables features like [Twitch's Dual-Format Vertical Video](https://help.twitch.tv/s/article/dual-format-vertical-video) where both a landscape desktop stream and a portrait mobile stream are delivered together.

Each configured output gets its **own virtual display and browser instance** rendered at native dimensions, so responsive layouts work correctly (no scaling or cropping).  Audio is captured from the primary (first) track only, and shared across all tracks.

When only a single output is configured (or `STREAM_OUTPUTS` is not set), the container operates in legacy single-stream mode, producing standard FLV identical to v1.x behavior.

## Uses

- Quickly spinning up a test stream without needing to install anything other than a container runtime.
- Setting up a long running stream for a [Twitch Extension Review](https://dev.twitch.tv/docs/extensions/life-cycle/#review) when a test stream is needed.
- Setting up a way to broadcast an overlay [like YarpBot does for its status page](https://www.twitch.tv/yarpbot) without a GUI client.
- Streaming both horizontal and vertical formats simultaneously for Twitch Enhanced Broadcasting / Dual-Format.
- Other.... stuff (you figure it out).

## Dependencies

1. [Docker](https://www.docker.com/) (or some other container runtime like [containerd](https://containerd.io/))
2. .....That's it.  Why did we make this a list?

## Quick Start

### Start A Stream Using Default Settings (720p 30 FPS, single track)

```bash
docker run -e WEBPAGE_URL=https://url-of-website-i-want-to-stream.com -e RTMP_URL=rtmp://rtmp-endpoint.to/stream/to ghcr.io/zozman/stream-webpage-container
```

### Start A Multitrack Stream (Desktop + Vertical Mobile)

```bash
docker run \
  -e WEBPAGE_URL=https://url-of-website-i-want-to-stream.com \
  -e RTMP_URL=rtmp://rtmp-endpoint.to/stream/to \
  -e STREAM_OUTPUTS='[{"width":1920,"height":1080,"framerate":60,"videoBitrate":"6000k","name":"desktop-1080p60"},{"width":1080,"height":1920,"framerate":30,"videoBitrate":"4500k","name":"vertical-1080x1920"}]' \
  ghcr.io/zozman/stream-webpage-container
```

## Available Image Tags

> [!NOTE]
> All available images can be found on the repo's [container package](https://github.com/Zozman/stream-webpage-container/pkgs/container/stream-webpage-container) page.

- `latest`
   - Represents the latest [release](https://github.com/Zozman/stream-webpage-container/releases) and should be what you use if you don't know what to use.
- `v*`
   - Example: `v2.0.0`
   - Represents a specific [release](https://github.com/Zozman/stream-webpage-container/releases) and is the right choice if you want to update versions manually.
- `edge`
   - Represents the latest commit to `main` and is not recommended for daily use unless you want the latest build NOW.
- `sha-*`
   - Examples: `sha-df87ff2ac624eb2de65861dfa3b09844a3f0f3db`, `sha-df87ff2`
   - Every commit to `main` will have a tag corresponding to that commit's long and short SHA.

## Running From Source

> [!NOTE]
> For the following `make` commands, you should have [Docker](https://www.docker.com/) installed since they use `docker` and `docker compose` under the hood.

### Using Docker Compose

1. Clone the repository

2. Set environment variables (through copying [`.env.example`](./.env.example) to `.env` or other methods)

3. Start just the application:
```bash
make run
```

### Local Development With RTMP Test Server

To test with a local RTMP server (MediaMTX, supports Enhanced RTMP multitrack):

```bash
# Start with the included RTMP server
make dev

# The RTMP server will be available at:
# rtmp://localhost:1935/live/stream
# RTSP republish: rtsp://localhost:8554/live/stream
# HLS: http://localhost:8888/live/stream
```

### Testing With VLC

#### Single Track (Legacy Mode)

1. Start the development environment: `make dev`
2. Open VLC -> `Media` -> `Open Network Stream`
3. Enter: `rtmp://localhost:1935/live/stream`
4. The stream should play.

#### Multitrack (Enhanced RTMP)

1. Set `STREAM_OUTPUTS` in your `.env` file (see the example in `.env.example`)
2. Start the development environment: `make dev`
3. Open VLC -> `Media` -> `Open Network Stream`
4. Use the RTSP republish URL: `rtsp://localhost:8554/live/stream`
5. Once playing, use `Video` -> `Video Track` to switch between the different renditions (e.g., desktop vs. vertical).  Audio is shared across all tracks.

#### Verify With ffprobe

You can confirm the multitrack stream is working by running:

```bash
ffprobe -v quiet -print_format json -show_streams rtmp://localhost:1935/live/stream | jq '[.streams[] | {codec_type, width, height}]'
```

Expected output for a 3-track stream:
```json
[
  { "codec_type": "video", "width": 1920, "height": 1080 },
  { "codec_type": "video", "width": 1080, "height": 1920 },
  { "codec_type": "video", "width": 640, "height": 360 },
  { "codec_type": "audio", "width": 0, "height": 0 }
]
```

> [!NOTE]
> You need FFmpeg 8.0+ for `ffprobe` to properly demux Enhanced FLV multitrack streams.  Older versions will only see a single video track.

### Run Unit Tests

To run unit tests within a dockerized environment, run the following:

```bash
make test
```

This command will save coverage results to the `coverage` directory.

> [!NOTE]
> If you have [go](https://go.dev/) installed, you can also run `go test -v -coverprofile=coverage/coverage.out ./... && go tool cover -html=coverage/coverage.out -o coverage/coverage.html` to do the same thing locally.

### Using Docker Only

```bash
# Build the image
docker build -t stream-webpage .

# Run the container (single track)
docker run -e WEBPAGE_URL="https://example.com" \
           -e RTMP_URL="rtmp://your-server/live/stream" \
           -e RESOLUTION="1080p" \
           stream-webpage

# Run the container (multitrack)
docker run -e WEBPAGE_URL="https://example.com" \
           -e RTMP_URL="rtmp://your-server/live/stream" \
           -e STREAM_OUTPUTS='[{"width":1920,"height":1080,"framerate":60},{"width":1080,"height":1920,"framerate":30}]' \
           stream-webpage
```

> [!WARNING]
> While you **can** run this locally by compiling the go code and executing it, I wouldn't recommend it as you need to make sure all the dependencies such as Chrome, Xvfb, PulseAudio, and [FFmpeg 8.0+](https://ffmpeg.org) are there and reachable.  Plus, having this be a container's kind of the point.

## Status Checking

If an environmental variable such as `TWITCH_CHANNEL` (see below) is set, then the container will check that channel to make sure the stream is live and attempt to restart the stream if it is not.  This is so the stream can automatically be restarted for platforms that have maximum stream lengths (such as Twitch's [being 48 hours per stream](https://help.twitch.tv/s/article/broadcasting-guidelines?language=en_US)).

### Twitch

To enable status checking for Twitch, provide a `TWITCH_CHANNEL`, `TWITCH_CLIENT_ID`, and `TWITCH_CLIENT_SECRET` environmental variable (see below for details).

> [!NOTE]
> Currently Twitch is the only supported platform but you can always file a PR if you want another platform.

## Twitch Enhanced Broadcasting

[Enhanced Broadcasting](https://help.twitch.tv/s/article/multiple-encodes) allows you to send multiple video renditions (landscape + portrait, multiple quality levels) in a single stream so Twitch can offer viewers adaptive quality and [Dual-Format Vertical Video](https://help.twitch.tv/s/article/dual-format-vertical-video) without server-side transcoding.

### How It Works

1. You set `TWITCH_ENHANCED_BROADCASTING=true` and provide your `TWITCH_STREAM_KEY`.
2. On startup, the app calls Twitch's **Go Live API** (`GetClientConfiguration`) to negotiate what tracks to send.
3. Twitch responds with the exact resolutions, bitrates, and an authorized ingest URL.
4. The app launches one Chrome + Xvfb per track and streams them all over a single Enhanced RTMP v2 connection.

### Setup

```bash
# .env
TWITCH_ENHANCED_BROADCASTING=true
TWITCH_STREAM_KEY=live_123456_YourStreamKeyHere
WEBPAGE_URL=https://your-webpage.com

# Define your desired canvas dimensions — the Go Live API uses these as hints.
# Include a portrait entry (height > width) to get vertical mobile tracks.
STREAM_OUTPUTS='[{"width":1280,"height":720,"framerate":30,"videoBitrate":"3500k","name":"landscape"},{"width":720,"height":1280,"framerate":30,"videoBitrate":"3000k","name":"portrait"}]'
```

Then run:
```bash
docker compose up --build stream-webpage
```

### What STREAM_OUTPUTS Does In This Mode

When Enhanced Broadcasting is enabled, `STREAM_OUTPUTS` is **not** used directly for the final output tracks — Twitch's API decides those.  Instead, it's used to tell the API:

- **Primary canvas**: the largest landscape entry (width >= height) sets the source resolution and framerate.
- **Portrait canvas**: the first entry with height > width signals that you want vertical tracks.  The API requires at least 2 tracks per canvas, so portrait mode needs a minimum of 4 total tracks.
- **Max tracks**: set to the number of entries in `STREAM_OUTPUTS`.

If `STREAM_OUTPUTS` is not set, the API defaults to 1920x1080 at 60fps with 3 landscape tracks.

### Performance Tuning

Each track runs its own Chrome instance and x264 software encode.  Key variables:

| Variable | Default | Purpose |
|----------|---------|---------|
| `ENCODER_PRESET` | `ultrafast` | x264 preset; faster = less CPU but lower quality |

Tips:
- **Start with 720p@30fps** to keep CPU reasonable, then scale up.
- Chrome's software rendering caps at ~20-25fps for complex pages at 720p+.  Simple pages or lower resolutions will hit 30fps.
- If you have GPU-accelerated hosts, consider higher resolutions or slower encoder presets for better quality.

## Environmental Variables

- `ENCODER_PRESET`
   - String
   - Default: `ultrafast`
   - The x264 encoder preset.  Faster presets use less CPU but produce lower quality at the same bitrate.  Options from fastest to slowest: `ultrafast`, `superfast`, `veryfast`, `faster`, `fast`, `medium`.  With multitrack encoding, `ultrafast` is recommended to avoid frame drops unless you have significant CPU headroom.
- `FRAMERATE`
   - Enum
      - `30`
      - `60`
   - Default: `30`
   - Sets the framerate for the single-output fallback mode.  In multitrack mode (`STREAM_OUTPUTS`), each track can specify its own framerate; this value is used as the default for entries that omit it.
- `LOG_FORMAT`
   - Enum
      - `json`
      - `console`
   - Default: `json`
   - Sets the format for logs printed.
- `LOG_LEVEL`
   - Enum
      - `debug`
      - `info`
      - `warn`
      - `warning`
      - `error`
      - `dpanic`
      - `panic`
      - `fatal`
   - Default: `info`
   - Level of logs that are printed.  Defined by [`zap` log levels](https://pkg.go.dev/go.uber.org/zap/zapcore#Level).
- `PORT`
   - String
   - Default: `8080`
   - Port to run the health and metrics endpoint on.
- `RESOLUTION`
   - Enum
      - `360p`
      - `720p`
      - `1080p`
      - `2k`
   - Default: `720p`
   - Sets the resolution for the single-output fallback mode.  In multitrack mode (`STREAM_OUTPUTS`), each track specifies its own dimensions; this value is not used.
- `RTMP_URL`
   - String
   - Default: `rtmp://localhost:1935/live/stream`
   - RTMP endpoint to send the stream to.  All tracks (single or multitrack) are sent to this single URL.  If using a service such as [Twitch](https://help.twitch.tv/s/twitch-ingest-recommendation?language=en_US), be sure your stream key is at the end of it.
- `STATUS_CRON_SCHEDULE`
   - String
   - Default: `*/10 * * * *` (every 10 minutes)
   - Cron string to define how often to check the status of the stream if status checking is enabled.
- `STREAM_OUTPUTS`
   - JSON Array (String)
   - Optional.  When set, enables multitrack Enhanced RTMP mode.  Each entry in the array defines a video track with its own browser instance rendered at the specified dimensions.
   - Fields per entry:
      - `resolution` (String, optional): Resolution preset (`360p`, `720p`, `1080p`, `2k`).  Mutually exclusive with explicit `width`/`height`.
      - `width` (Integer, optional): Explicit pixel width.  Required if `resolution` is not set.
      - `height` (Integer, optional): Explicit pixel height.  Required if `resolution` is not set.
      - `framerate` (Integer, optional): Frames per second.  Defaults to `FRAMERATE` env value or 30.
      - `videoBitrate` (String, optional): Video bitrate (e.g. `"6000k"`).  Auto-derived from dimensions/framerate if omitted.
      - `name` (String, optional): Human-readable label for the track.
   - The **first entry** is the primary track (its browser produces the shared audio).
   - Example (desktop + Twitch vertical):
     ```json
     [
       {"width":1920,"height":1080,"framerate":60,"videoBitrate":"6000k","name":"desktop"},
       {"width":1080,"height":1920,"framerate":30,"videoBitrate":"4500k","name":"vertical"}
     ]
     ```
- `TWITCH_CHANNEL`
   - String
   - If provided a value, the application will attempt to check the status of the stream at the provided channel as per the `STATUS_CRON_SCHEDULE` and will restart the stream if it is detected to not be live.
   - Requires the `TWITCH_CLIENT_ID` and `TWITCH_CLIENT_SECRET` to be defined to work properly.
- `TWITCH_CLIENT_ID`
   - String
   - Twitch Client ID obtained from the [Twitch Developer Console](https://dev.twitch.tv/console) for checking stream status if the `TWITCH_CHANNEL` environmental variable is set.
   - Checking for the stream status on Twitch will not work without this and `TWITCH_CLIENT_SECRET` being set.
   - For more information about registering an app on Twitch, see [the developer documentation](https://dev.twitch.tv/docs/authentication/register-app/).
- `TWITCH_CLIENT_NAME`
   - String
   - Default: `stream-webpage-container`
   - Overrides the `client.name` field sent to Twitch's Go Live API.  Only relevant when `TWITCH_ENHANCED_BROADCASTING=true`.
- `TWITCH_CLIENT_SECRET`
   - String
   - Twitch Client Secret obtained from the [Twitch Developer Console](https://dev.twitch.tv/console) for checking stream status if the `TWITCH_CHANNEL` environmental variable is set.
   - Checking for the stream status on Twitch will not work without this and `TWITCH_CLIENT_ID` being set.
   - For more information about registering an app on Twitch, see [the developer documentation](https://dev.twitch.tv/docs/authentication/register-app/).
- `TWITCH_ENHANCED_BROADCASTING`
   - String (`true` / `false`)
   - Default: not set (disabled)
   - When set to `true`, enables Twitch Enhanced Broadcasting mode.  The app calls Twitch's [Go Live API](https://docs.aws.amazon.com/ivs/latest/LowLatencyUserGuide/multitrack-video-sw-integration.html) (`GetClientConfiguration`) before streaming to get server-authorized multitrack configuration.  When active, `RTMP_URL` is ignored — the server provides the ingest URL.  `STREAM_OUTPUTS` is still read to determine canvas dimensions and orientation for the API request.
   - Requires `TWITCH_STREAM_KEY` to be set.
- `TWITCH_STREAM_KEY`
   - String
   - Your Twitch stream key (e.g. `live_123456_abcdef`).  Required when `TWITCH_ENHANCED_BROADCASTING=true`.
   - Obtain from your [Twitch Dashboard](https://dashboard.twitch.tv/settings/stream) under Settings -> Stream.
- `WEBPAGE_REFRESH_INTERVAL`
   - String
   - If set to a positive integer, all browser instances will automatically refresh the webpage at the specified interval in seconds. This can help prevent issues with stale content or memory leaks during long streaming sessions.
   - If not set or set to an invalid value, automatic refresh is disabled.
- `WEBPAGE_URL`
   - String
   - Default: `https://google.com`
   - The webpage to stream.  All tracks render this same URL; the page is expected to adapt responsively to each viewport size.

## Resource Considerations

Each configured output runs its own Xvfb display server and Chrome browser instance.  This means CPU and memory usage scales with the number of tracks:

- **1 track**: Similar to v1.x resource usage.
- **2 tracks**: Approximately 2x browser memory, increased CPU for encoding.
- **3+ tracks**: Plan accordingly; monitor container resource limits.

This is the same trade-off that OBS makes with Enhanced Broadcasting (multiple client-side encodes).

## Compatibility Notes

- **Enhanced RTMP Servers**: The multitrack stream works with any server supporting Enhanced RTMP v2 (MediaMTX, Amazon IVS, OvenMediaEngine).
- **Twitch Enhanced Broadcasting**: Set `TWITCH_ENHANCED_BROADCASTING=true` and `TWITCH_STREAM_KEY` to enable full Twitch Enhanced Broadcasting with automatic stream configuration.  The app calls Twitch's Go Live API to negotiate track settings and obtain authorized ingest credentials.  Use `STREAM_OUTPUTS` to define your desired canvas dimensions and orientation (portrait entries with height > width automatically enable vertical tracks).  The server decides the final track layout based on your preferences and channel configuration.
- **YouTube/Other Platforms**: Most platforms that accept RTMP will work in single-track mode.  Multitrack support varies by platform.
- **VLC Playback**: Use the RTSP republish from MediaMTX (`rtsp://localhost:8554/...`) for track selection in VLC.  Direct RTMP playback in VLC may only show the first track depending on your VLC version.

## About

Cobbled together by [Zac Lovoy](https://bsky.app/profile/zwlovoy.bsky.social) (aka [BigZoz on Twitch](https://www.twitch.tv/bigzoz)).  If you're interested in some of other streaming adjacent stuff, check out [YarpBot](https://yarpbot.com) or the [Filters Extension for Twitch](https://dashboard.twitch.tv/extensions/npqfekui52xl3nuuk91h2pmrszod57).

Or whatever; I'm not your dad.
