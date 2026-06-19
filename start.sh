#!/bin/bash
set -e

echo "=== Setting Up Displays ==="

# Clean up any existing X server and choose a random display
pkill Xvfb || true
rm -f /tmp/.X*-lock

DISPLAY_BASE=${DISPLAY_BASE:-100}
DISPLAY_MAP=""
PRIMARY_DISPLAY=""

while IFS='|' read -r TARGET_NAME TARGET_WIDTH TARGET_HEIGHT; do
    if [ -z "$TARGET_NAME" ]; then
        continue
    fi

    CURRENT_DISPLAY=":$DISPLAY_BASE"
    SCREEN_RESOLUTION="${TARGET_WIDTH}x${TARGET_HEIGHT}"
    echo "Starting X server for ${TARGET_NAME} on display ${CURRENT_DISPLAY} at ${SCREEN_RESOLUTION}"

    Xvfb "${CURRENT_DISPLAY}" -screen 0 "${SCREEN_RESOLUTION}x24" -ac +extension GLX +render -noreset &

    if [ -z "$PRIMARY_DISPLAY" ]; then
        PRIMARY_DISPLAY="$CURRENT_DISPLAY"
    fi

    if [ -n "$DISPLAY_MAP" ]; then
        DISPLAY_MAP="${DISPLAY_MAP},"
    fi
    DISPLAY_MAP="${DISPLAY_MAP}${TARGET_NAME}=${CURRENT_DISPLAY}"

    DISPLAY_BASE=$((DISPLAY_BASE + 1))
done < <(/stream render-targets)

if [ -z "$DISPLAY_MAP" ]; then
    echo "Failed to determine render targets"
    exit 1
fi

export DISPLAY="$PRIMARY_DISPLAY"
export STREAM_RENDER_DISPLAYS="$DISPLAY_MAP"

echo "Using render target displays: $STREAM_RENDER_DISPLAYS"
sleep 3

for DISPLAY_ENTRY in ${STREAM_RENDER_DISPLAYS//,/ }; do
    CURRENT_DISPLAY="${DISPLAY_ENTRY#*=}"
    while ! xdpyinfo -display "$CURRENT_DISPLAY" >/dev/null 2>&1; do
        echo "Waiting for X server ${CURRENT_DISPLAY} to start..."
        sleep 1
    done
done
echo "X servers ready"

echo "=== Setting Up Audio ==="

# Start D-Bus for PulseAudio
mkdir -p /var/run/dbus
dbus-daemon --config-file=/usr/share/dbus-1/system.conf --print-address &
sleep 2

# Start PulseAudio
pulseaudio --kill 2>/dev/null || true
sleep 1
export PULSE_RUNTIME_PATH=/var/run/pulse
mkdir -p $PULSE_RUNTIME_PATH
pulseaudio --start --log-target=syslog --system=false &
sleep 3

# Create a null sink for audio output
pactl load-module module-null-sink sink_name=null_output sink_properties=device.description=Null_Output 2>/dev/null || echo "Failed to create null sink"

# Set the null sink as default
pactl set-default-sink null_output 2>/dev/null || echo "Failed to set default sink"

# Create ALSA configuration that uses the default PulseAudio device
cat > /root/.asoundrc << EOF
pcm.!default {
    type pulse
}
ctl.!default {
    type pulse
}
EOF

# Debug: List available audio devices
echo "=== Audio Setup Debug ==="
echo "PulseAudio sinks:"
pactl list short sinks 2>/dev/null || echo "PulseAudio not running"
echo "PulseAudio sources:"
pactl list short sources 2>/dev/null || echo "No PulseAudio sources"
echo "ALSA devices:"
aplay -l 2>/dev/null || echo "No ALSA playback devices found"

# Wait for audio system to stabilize
sleep 2

echo "=== Verifying System Readiness ==="

# Wait for X server to be fully functional
echo "Waiting for X server to be fully ready..."
while ! xdpyinfo -display :$DISPLAY_NUM >/dev/null 2>&1; do
    echo "X server not yet ready, waiting..."
    sleep 2
done
echo "✓ X server is ready"

# Wait for PulseAudio to be fully functional
echo "Waiting for PulseAudio to be ready..."
while ! pactl info >/dev/null 2>&1; do
    echo "PulseAudio not yet ready, waiting..."
    sleep 2
done
echo "✓ PulseAudio is responding"

echo "=== All Dependencies Ready - Starting Application ==="
# Start the stream application
exec /stream
