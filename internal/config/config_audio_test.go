package config

import "testing"

func TestServerAudioDefaultsApplied(t *testing.T) {
	cfg := ServerConfig{
		BindHost: "0.0.0.0",
		Port:     5000,
	}
	cfg.Capture.Backend = CaptureBackendAuto
	cfg.Capture.FPS = 60
	cfg.Capture.Width = 1920
	cfg.Capture.Height = 1080
	cfg.Capture.Codec = "h264"
	cfg.Capture.H264CodecConfig = map[string]interface{}{
		"bitrate": 5000,
		"preset":  "ultrafast",
		"tune":    "zerolatency",
	}
	cfg.StatsIntervalMS = 1000

	applyServerCompat(&cfg, serverConfigCompat{})

	if cfg.Audio.Codec != "opus" {
		t.Fatalf("expected default audio codec opus, got %q", cfg.Audio.Codec)
	}
	if cfg.Audio.SampleRate != 48000 || cfg.Audio.Channels != 2 || cfg.Audio.FrameMS != 20 || cfg.Audio.BitrateKbps != 96 {
		t.Fatalf("unexpected audio defaults: %+v", cfg.Audio)
	}
}

func TestServerValidateAudioEnabled(t *testing.T) {
	cfg := ServerConfig{}
	cfg.BindHost = "0.0.0.0"
	cfg.Port = 5000
	cfg.Capture.Backend = CaptureBackendAuto
	cfg.Capture.FPS = 60
	cfg.Capture.Width = 1920
	cfg.Capture.Height = 1080
	cfg.Capture.Codec = "h264"
	cfg.Capture.H264CodecConfig = map[string]interface{}{
		"bitrate": 5000,
		"preset":  "ultrafast",
		"tune":    "zerolatency",
	}
	cfg.StatsIntervalMS = 1000
	cfg.Audio.Enabled = true
	cfg.Audio.Codec = "opus"
	cfg.Audio.SampleRate = 48000
	cfg.Audio.Channels = 2
	cfg.Audio.FrameMS = 20
	cfg.Audio.BitrateKbps = 96

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid audio config, got error: %v", err)
	}

	cfg.Audio.Codec = "aac"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for non-opus codec")
	}
}
