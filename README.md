# Stream Webpage Container

A containerized application to stream a webpage live over RTMP.  Just pass a `WEBPAGE_URL` and a `RTMP_URL` and the container will open a browser, capture the video and audio, and send it to the specified location.  It can even be configured to automatically restart the stream for supported services.

## Uses

- Quickly spinning up a test stream without needing to install anything other than a container runtime.
- Setting up a long running stream for a [Twitch Extension Review](https://dev.twitch.tv/docs/extensions/life-cycle/#review) when a test stream is needed.
- Setting up a way to broadcast an overlay [like YarpBot does for its status page](https://www.twitch.tv/yarpbot) without a GUI client.
- Other.... stuff (you figure it out).

## Dependencies

1. [Docker](https://www.docker.com/) (or some other container runtime like [containerd](https://containerd.io/))
2. .....That's it.  Why did we make this a list?

## Quick Start

### Start A Stream Using Default Settings (720p 30 FPS)

`docker run -e WEBPAGE_URL=https://url-of-website-i-want-to-stream.com -e RTMP_URL=rtmp://rtmp-endpoint.to/stream/to ghcr.io/zozman/stream-webpage-container`

### Start A Stream At 1080p 60 FPS

`docker run -e WEBPAGE_URL=https://url-of-website-i-want-to-stream.com -e RTMP_URL=rtmp://rtmp-endpoint.to/stream/to -e RESOLUTION=720p -e FRAMERATE=60 ghcr.io/zozman/stream-webpage-container`

## Available Image Tags

> [!NOTE]
> All available images can be found on the repo's [container package](https://github.com/Zozman/stream-webpage-container/pkgs/container/stream-webpage-container) page.

- `latest`
   - Represents the latest [release](https://github.com/Zozman/stream-webpage-container/releases) and should be what you use if you don't know what to use.
- `v*`
   - Example: `v1.0.0`
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

To test with a local RTMP server:

```bash
# Start with the included RTMP server
make dev

# The RTMP server will be available at:
# rtmp://localhost:1935/live/stream
```

You can then use a program like VLC to view the stream to ensure it works (use `Media` -> `Open Network Stream` and use the address `rtmp://localhost:1935/live/stream` for this example).

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

# Run the container
docker run -e WEBPAGE_URL="https://example.com" \
           -e RTMP_URL="rtmp://your-server/live/stream" \
           -e RESOLUTION="1080p" \
           stream-webpage
```

> [!WARNING]
> While you **can** run this locally by compiling the go code and executing it, I wouldn't recommend it as you need to make sure all the dependencies such as Chrome and [ffmpeg](https://ffmpeg.org) are there and reachable.  Plus, having this be a container's kind of the point.

## Status Checking

If an environmental variable such as `TWITCH_CHANNEL` (see below) is set, then the container will check that channel to make sure the stream is live and attempt to restart the stream if it is not.  This is so the stream can automatically be restarted for platforms that have maximum stream lengths (such as Twitch's [being 48 hours per stream](https://help.twitch.tv/s/article/broadcasting-guidelines?language=en_US)).

### Twitch

To enable status checking for Twitch, provide a `TWITCH_CHANNEL`, `TWITCH_CLIENT_ID`, and `TWITCH_CLIENT_SECRET` environmental variable (see below for details).

> [!NOTE]
> Currently Twitch is the only supported platform but you can always file a PR if you want another platform.

## Environmental Variables

- `FRAMERATE`
   - Enum
      - `30`
      - `60`
   - Default: `30`
   - Sets the framerate of the stream.  Currently supports `30` or `60` frames per second.
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
- `STATUS_CRON_SCHEDULE`
   - String
   - Default: `*/10 * * * *` (every 10 minutes)
   - Cron string to define how often to check the status of the stream if status checking is enabled.
- `RESOLUTION`
   - Enum
      - `720p`
      - `1080p`
      - `2k`
   - Default: `720p`
   - What resolution the RTMP stream should be.
- `RTMP_URL`
   - String
   - Default: `rtmp://localhost:1935/live/stream`
   - RMTP endpoint to send the stream to.  If using a service such as [Twitch](https://help.twitch.tv/s/twitch-ingest-recommendation?language=en_US), be sure your stream key is at the end of it.
- `TWITCH_CHANNEL`
   - String
   - If provided a value, the application will attempt to check the status of the stream at the provided channel as per the `STATUS_CRON_SCHEDULE` and will restart the stream if it is detected to not be live.
   - Requires the `TWITCH_CLIENT_ID` and `TWITCH_CLIENT_SECRET` to be defined to work properly.
- `TWITCH_CLIENT_ID`
   - String
   - Twitch Client ID obtained from the [Twitch Developer Console](https://dev.twitch.tv/console) for checking stream status if the `TWITCH_CHANNEL` environmental variable is set.
   - Checking for the stream status on Twitch will not work without this and `TWITCH_CLIENT_SECRET` being set.
   - For more information about registering an app on Twitch, see [the developer documentation](https://dev.twitch.tv/docs/authentication/register-app/).
- `TWITCH_CLIENT_SECRET`
   - String
   - Twitch Client ID obtained from the [Twitch Developer Console](https://dev.twitch.tv/console) for checking stream status if the `TWITCH_CHANNEL` environmental variable is set.
   - Checking for the stream status on Twitch will not work without this and `TWITCH_CLIENT_ID` being set.
   - For more information about registering an app on Twitch, see [the developer documentation](https://dev.twitch.tv/docs/authentication/register-app/).
- `WEBPAGE_URL`
   - String
   - Default: `https://google.com`
   - The webpage to stream.

## About

Cobbled together by [Zac Lovoy](https://bsky.app/profile/zwlovoy.bsky.social) (aka [BigZoz on Twitch](https://www.twitch.tv/bigzoz)).  If you're interested in some of other streaming adjacent stuff, check out [YarpBot](https://yarpbot.com) or the [Filters Extension for Twitch](https://dashboard.twitch.tv/extensions/npqfekui52xl3nuuk91h2pmrszod57).

Or whatever; I'm not your dad.