//go:build linux

package capture

import (
	"testing"

	"streamscreen/internal/config"
)

func TestNewLinuxSourceBackendSelection(t *testing.T) {
	cfg := config.ServerConfig{}

	src, err := New(cfg, config.CaptureBackendPortalPipewire)
	if err != nil {
		t.Fatalf("expected portal backend to be supported, got err=%v", err)
	}
	if src == nil {
		t.Fatalf("expected non-nil source")
	}

	if _, err := New(cfg, config.CaptureBackendDDAGrab); err == nil {
		t.Fatalf("expected ddagrab backend to be rejected on linux")
	}
}
