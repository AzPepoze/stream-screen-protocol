# Stream Screen Protocol

## Usage

### Build

```bash
make build
```

### Run

**Server:**
```bash
./bin/server
```

**Client:**
```bash
./bin/client
```

## Configuration

### server.config.yaml

```yaml
# Network binding
bind_host: "0.0.0.0"              # Server bind address
port: 7700                        # Server UDP port

# Screen Capture Settings
capture:
  backend: "auto"                 # auto, dxgi, portal-pipewire, ddagrab, gdigrab
  fps: 100                        # Frames per second
  width: 1920                     # Video width
  height: 1080                    # Video height
  source: "default"               # Capture source display / monitor
  cursor_mode: "embedded"         # embedded, hidden, metadata
  source_type: "monitor"          # monitor, window, virtual, any
  codec: "rgba"                   # rgba, h264
  rgba_codec_config:
    tile_size: 20                 # Tile size for RGBA compression
  h264_codec_config:
    bitrate: 8000                 # Bitrate in kbps
    preset: "ultrafast"           # ultrafast, superfast, veryfast, faster, fast, medium
    tune: "zerolatency"           # zerolatency, film, animation

# Server stats reporting interval
stats_interval_ms: 1000

# Audio streaming configuration
audio:
  enabled: true                   # Enable audio streaming
  codec: "opus"                   # Audio codec (opus)
  sample_rate: 48000              # Sample rate in Hz
  channels: 2                     # Number of channels (1=mono, 2=stereo)
  frame_ms: 20                    # Frame size in milliseconds
  bitrate_kbps: 96                # Audio bitrate in kbps
  audio_input_device: "interactive" # Audio input device or interactive selector
```

### client.config.yaml

```yaml
# Target server connection
server_host: "127.0.0.1"          # Server address
port: 7700                        # Server UDP port

# Client window display
window:
  title: "StreamScreen Client"    # Window title
  width: 1280                     # Window fallback width
  height: 720                     # Window fallback height
  fullscreen: false               # Fullscreen mode

# Performance & stats HUD
stats:
  debug: true                     # Show debug stats overlay (FPS, latency, bitrate)
  font_size: 16                   # Stats font size
  update_interval_ms: 500         # Stats update interval in ms

# Network resilience & jitter buffer
network:
  max_latency_ms: 200             # Max acceptable latency in ms
  nack_retry_ms: 20               # NACK retry interval in ms
  partial_frame_ready: 0.98       # Threshold fraction for partial frames (0.0-1.0)
  allow_partial_frames: true      # Allow rendering incomplete frames
  force_output_partial: true      # Force partial frame output on deadline
  auto_tune_by_fps: true          # Auto tune buffer based on FPS

# Audio playback
audio:
  enabled: true                   # Enable audio playback
  audio_output_device: "interactive" # Audio output device or interactive selector
```
