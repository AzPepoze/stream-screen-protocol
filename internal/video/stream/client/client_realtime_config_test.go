package client

import (
	"testing"

	"streamscreen/internal/config"
	"streamscreen/internal/video/stream"
)

func TestClientHandleVideoInfoModeSwitch(t *testing.T) {
	cfg := config.ClientConfig{
		ServerHost: "127.0.0.1",
		Port:       17799,
	}
	cfg.Window.Width = 1280
	cfg.Window.Height = 720
	cfg.Network.MaxLatencyMS = 200
	cfg.Network.NackRetryMS = 20
	cfg.Network.PartialFrameReady = 0.98
	cfg.Network.AllowPartial = true
	cfg.Network.ForceOutput = true

	receiver, err := NewClientReceiver(cfg)
	if err != nil {
		t.Fatalf("failed to create client receiver: %v", err)
	}
	defer func() { _ = receiver.Stop() }()

	// Initial video info: 1280x720 @ 60 fps, gridSize=20, codec=rgba
	info1 := stream.MarshalVideoInfo(1280, 720, 60, 20, "rgba")
	receiver.handleVideoInfo(info1, len(info1))

	w, h := receiver.GetVideoResolution()
	if w != 1280 || h != 720 {
		t.Fatalf("expected 1280x720, got %dx%d", w, h)
	}
	if receiver.GetVideoFPS() != 60 {
		t.Fatalf("expected 60 fps, got %d", receiver.GetVideoFPS())
	}
	if receiver.GetCodecName() != "rgba" {
		t.Fatalf("expected codec rgba, got %s", receiver.GetCodecName())
	}

	// Dynamic mode switch: 1920x1080 @ 100 fps, gridSize=10, codec=h264
	info2 := stream.MarshalVideoInfo(1920, 1080, 100, 10, "h264")
	receiver.handleVideoInfo(info2, len(info2))

	w2, h2 := receiver.GetVideoResolution()
	if w2 != 1920 || h2 != 1080 {
		t.Fatalf("expected updated resolution 1920x1080, got %dx%d", w2, h2)
	}
	if receiver.GetVideoFPS() != 100 {
		t.Fatalf("expected updated fps 100, got %d", receiver.GetVideoFPS())
	}
	if receiver.GetCodecName() != "h264" {
		t.Fatalf("expected updated codec h264, got %s", receiver.GetCodecName())
	}

	// Verify canvas refresh flag was set
	if !receiver.ConsumeCanvasRefresh() {
		t.Fatalf("expected ConsumeCanvasRefresh to return true after mode switch")
	}
	// Second call should return false
	if receiver.ConsumeCanvasRefresh() {
		t.Fatalf("expected ConsumeCanvasRefresh to return false after consumption")
	}

	// Verify pixel buffer was resized to 1920*1080*4
	pixels, _ := receiver.Pixels()
	expectedPixelSize := 1920 * 1080 * 4
	if len(pixels) != expectedPixelSize {
		t.Fatalf("expected pixel buffer size %d, got %d", expectedPixelSize, len(pixels))
	}
}
