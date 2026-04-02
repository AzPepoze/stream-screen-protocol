//go:build windows

package capture

import (
	"context"
	"testing"
	"time"

	"streamscreen/internal/config"
)

func TestDXGISource(t *testing.T) {
	cfg := config.ServerConfig{}
	cfg.Capture.Width = 1920
	cfg.Capture.Height = 1080
	cfg.Capture.FPS = 30
	cfg.Capture.Source = "0" // Primary monitor

	src, err := newDXGISource(cfg)
	if err != nil {
		t.Skipf("DXGI not available: %v", err)
		return
	}
	defer src.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = src.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start DXGI capture: %v", err)
	}

	// Wait for a few frames
	framesReceived := 0
	timeout := time.After(2 * time.Second)

	for framesReceived < 3 {
		select {
		case frame := <-src.Frames():
			if len(frame) == 0 {
				t.Error("Received empty frame")
				continue
			}
			expectedSize := cfg.Capture.Width * cfg.Capture.Height * 4 // BGRA8
			if len(frame) != expectedSize {
				t.Errorf("Frame size mismatch: got %d, expected %d", len(frame), expectedSize)
			}
			framesReceived++
		case <-timeout:
			t.Fatalf("Timeout waiting for frames, received %d frames", framesReceived)
		}
	}
}