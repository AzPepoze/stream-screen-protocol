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

### server.config.json

```json
{
  "bind_host": "0.0.0.0",              // Server bind address
  "client_host": "127.0.0.1",          // Client connection address
  "port": 7700,                        // Server port
  "capture": {
    "backend": "auto",                 // auto, x11, windows
    "fps": 100,                        // Frames per second
    "width": 1920,                     // Video width
    "height": 1080,                    // Video height
    "source": "default",               // Capture source (X11 display, etc)
    "cursor_mode": "embedded",         // embedded, overlay, hidden
    "source_type": "monitor",          // monitor, window
    "codec": "rgba",                   // rgba, h264
    "rgba_codec_config": {
      "tile_size": 20                  // Tile size for RGBA compression
    },
    "h264_codec_config": {
      "bitrate": 8000,                 // Bitrate in kbps
      "preset": "ultrafast",           // ultrafast, superfast, veryfast, faster, fast, medium
      "tune": "zerolatency"            // zerolatency, fastdecode
    }
  },
  "stats_interval_ms": 1000,           // Stats reporting interval
  "audio": {
    "enabled": true,                   // Enable audio streaming
    "codec": "opus",                   // Audio codec
    "sample_rate": 48000,              // Sample rate in Hz
    "channels": 2,                     // Number of channels
    "frame_ms": 20,                    // Frame size in milliseconds
    "bitrate_kbps": 96,                // Audio bitrate in kbps
    "audio_input_device": "interactive" // Audio input device
  }
}
```

### client.config.json

```json
{
  "server_host": "127.0.0.1",          // Server address
  "port": 7700,                        // Server port
  "window": {
    "title": "StreamScreen Client",    // Window title
    "width": 1280,                     // Window width
    "height": 720,                     // Window height
    "fullscreen": false                // Fullscreen mode
  },
  "stats": {
    "debug": true,                     // Show debug stats overlay
    "font_size": 16,                   // Stats font size
    "update_interval_ms": 500          // Stats update interval
  },
  "network": {
    "max_latency_ms": 200,             // Max acceptable latency
    "nack_retry_ms": 20,               // NACK retry interval
    "partial_frame_ready": 0.98,       // Threshold for partial frames (0-1)
    "allow_partial_frames": true,      // Allow rendering incomplete frames
    "force_output_partial": true,      // Force partial frame output
    "auto_tune_by_fps": true           // Auto tune based on FPS
  },
  "audio": {
    "enabled": true,                   // Enable audio playback
    "audio_output_device": "interactive" // Audio output device
  }
}
```
