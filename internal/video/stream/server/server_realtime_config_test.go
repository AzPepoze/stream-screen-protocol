package server

import (
	"testing"

	"streamscreen/internal/config"
)

func TestServerUpdateVideoConfig(t *testing.T) {
	cfg := config.ServerConfig{
		BindHost: "127.0.0.1",
		Port:     17788,
	}
	cfg.Capture.Backend = config.CaptureBackendAuto
	cfg.Capture.FPS = 60
	cfg.Capture.Width = 1280
	cfg.Capture.Height = 720
	cfg.Capture.Codec = "rgba"
	cfg.Capture.RGBACodecConfig = map[string]interface{}{"tile_size": 20}
	cfg.StatsIntervalMS = 1000

	sender, err := NewSender(cfg)
	if err != nil {
		t.Fatalf("failed to create sender: %v", err)
	}
	defer func() { _ = sender.Stop() }()

	if sender.codecName != "rgba" {
		t.Fatalf("expected initial codec rgba, got %s", sender.codecName)
	}
	if sender.tileBuffer == nil || sender.tileBuffer.gridSize != 20 {
		t.Fatalf("expected tile buffer grid size 20, got %+v", sender.tileBuffer)
	}

	// Update config to 1920x1080 @ 100 fps, h264
	newCfg := cfg
	newCfg.Capture.FPS = 100
	newCfg.Capture.Width = 1920
	newCfg.Capture.Height = 1080
	newCfg.Capture.Codec = "h264"
	newCfg.Capture.H264CodecConfig = map[string]interface{}{
		"bitrate": 8000,
		"preset":  "ultrafast",
		"tune":    "zerolatency",
	}

	if err := sender.UpdateVideoConfig(newCfg); err != nil {
		t.Fatalf("UpdateVideoConfig failed: %v", err)
	}

	if sender.codecName != "h264" {
		t.Fatalf("expected switched codec h264, got %s", sender.codecName)
	}
	if sender.cfg.Capture.FPS != 100 || sender.cfg.Capture.Width != 1920 || sender.cfg.Capture.Height != 1080 {
		t.Fatalf("expected updated capture config: %+v", sender.cfg.Capture)
	}

	// Update back to RGBA with tile_size 10
	newCfg2 := newCfg
	newCfg2.Capture.Codec = "rgba"
	newCfg2.Capture.RGBACodecConfig = map[string]interface{}{"tile_size": 10}

	if err := sender.UpdateVideoConfig(newCfg2); err != nil {
		t.Fatalf("UpdateVideoConfig back to rgba failed: %v", err)
	}

	if sender.codecName != "rgba" {
		t.Fatalf("expected switched codec rgba, got %s", sender.codecName)
	}
	if sender.tileBuffer == nil || sender.tileBuffer.gridSize != 10 {
		t.Fatalf("expected tile buffer grid size 10, got %+v", sender.tileBuffer)
	}

	// Process RGBA frame should work without panic
	frameData := make([]byte, 1920*1080*4)
	sender.ProcessRGBAFrame(frameData)
}
