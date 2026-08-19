package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServerYAMLWithComments(t *testing.T) {
	yamlData := `
# Server network binding
bind_host: "0.0.0.0" # Listen on all interfaces
port: 7700           # Server UDP port

# Screen capture settings
capture:
  backend: "auto"    # Auto-detect best backend (dxgi, portal-pipewire, etc.)
  fps: 60            # Capture framerate
  width: 1920        # Stream resolution width
  height: 1080       # Stream resolution height
  source: "default"  # Monitor / source name
  cursor_mode: "embedded" # hidden, embedded, or metadata
  source_type: "monitor"  # monitor, window, virtual, any
  codec: "h264"      # Video codec: h264 or rgba
  rgba_codec_config:
    tile_size: 20    # Tile size for delta compression
  h264_codec_config:
    bitrate: 8000    # Target bitrate in kbps
    preset: "ultrafast" # x264 speed preset
    tune: "zerolatency"  # Tuning flag

# Server statistics interval
stats_interval_ms: 1000

# Audio streaming configuration
audio:
  enabled: true      # Enable audio streaming
  codec: "opus"      # Audio codec (opus)
  sample_rate: 48000 # Sample rate in Hz
  channels: 2        # Stereo audio
  frame_ms: 20       # Audio frame duration in ms
  bitrate_kbps: 96   # Audio bitrate
  audio_input_device: "interactive" # Default audio input or interactive selector
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "server.config.yaml")
	if err := os.WriteFile(filePath, []byte(yamlData), 0644); err != nil {
		t.Fatalf("failed to write tmp yaml: %v", err)
	}

	cfg, err := LoadServer(filePath)
	if err != nil {
		t.Fatalf("failed to load server yaml config: %v", err)
	}

	if cfg.BindHost != "0.0.0.0" || cfg.Port != 7700 {
		t.Fatalf("unexpected host/port: %s:%d", cfg.BindHost, cfg.Port)
	}
	if cfg.Capture.FPS != 60 || cfg.Capture.Width != 1920 || cfg.Capture.Height != 1080 {
		t.Fatalf("unexpected capture settings: %+v", cfg.Capture)
	}
	if cfg.Capture.Codec != "h264" {
		t.Fatalf("unexpected codec: %s", cfg.Capture.Codec)
	}
	if cfg.Audio.Enabled != true || cfg.Audio.BitrateKbps != 96 {
		t.Fatalf("unexpected audio config: %+v", cfg.Audio)
	}
}

func TestLoadClientYAMLWithComments(t *testing.T) {
	yamlData := `
# StreamScreen Client Configuration
server_host: "127.0.0.1" # Target server IP or hostname
port: 7700               # Target server UDP port

# Render Window Settings
window:
  title: "StreamScreen Client" # Window title
  width: 1280                  # Fallback window width
  height: 720                  # Fallback window height
  fullscreen: false            # Start in fullscreen mode

# Performance Stats Overlay
stats:
  debug: true                  # Show on-screen stats overlay (FPS, latency, bitrate)
  font_size: 16                # Stats text font size
  update_interval_ms: 500      # Stats recalculation interval in ms

# Network Resilience & Jitter Buffer
network:
  max_latency_ms: 200          # Maximum tolerated jitter buffer latency
  nack_retry_ms: 20            # Retransmission retry interval
  partial_frame_ready: 0.98    # Threshold fraction to trigger partial frame rendering
  allow_partial_frames: true   # Enable partial/incomplete frame rendering
  force_output_partial: true   # Force output of partial frames on timeout
  auto_tune_by_fps: true       # Dynamic buffer adaptation based on incoming FPS

# Audio Playback
audio:
  enabled: true                # Enable audio playback
  audio_output_device: "interactive" # Output device or interactive selector
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "client.config.yaml")
	if err := os.WriteFile(filePath, []byte(yamlData), 0644); err != nil {
		t.Fatalf("failed to write tmp yaml: %v", err)
	}

	cfg, err := LoadClient(filePath)
	if err != nil {
		t.Fatalf("failed to load client yaml config: %v", err)
	}

	if cfg.ServerHost != "127.0.0.1" || cfg.Port != 7700 {
		t.Fatalf("unexpected host/port: %s:%d", cfg.ServerHost, cfg.Port)
	}
	if !cfg.Stats.Debug || cfg.Stats.FontSize != 16 {
		t.Fatalf("unexpected stats config: %+v", cfg.Stats)
	}
	if !cfg.Network.AllowPartial || cfg.Network.PartialFrameReady != 0.98 {
		t.Fatalf("unexpected network config: %+v", cfg.Network)
	}
	if !cfg.Audio.Enabled || cfg.Audio.OutputDevice != "interactive" {
		t.Fatalf("unexpected audio config: %+v", cfg.Audio)
	}
}

func TestMissingConfigFileReturnsError(t *testing.T) {
	_, err := LoadServer("nonexistent.config.yaml")
	if err == nil {
		t.Fatalf("expected error for nonexistent config file")
	}
}
