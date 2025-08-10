#!/bin/bash
set -e

echo "=== Setting Up Display ==="

# Clean up any existing X server and choose a random display
pkill Xvfb || true
rm -f /tmp/.X*-lock
DISPLAY_NUM=$((RANDOM % 100 + 100))  # Random display between 100-199
export DISPLAY=:$DISPLAY_NUM

echo "Starting X server on display $DISPLAY"
Xvfb :$DISPLAY_NUM -screen 0 1920x1080x24 -ac +extension GLX +render -noreset &
sleep 3

# Wait for X server to be ready
while ! xdpyinfo -display :$DISPLAY_NUM >/dev/null 2>&1; do
    echo "Waiting for X server to start..."
    sleep 1
done
echo "X server ready"

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

echo "=== Starting application ==="
# Start the stream application
exec /stream
