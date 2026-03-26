//go:build linux

package platform

import (
	"testing"

	"streamscreen/internal/config"
)

func TestPrepareBackendLinux(t *testing.T) {
	cfg := config.ServerConfig{}
	cfg.Capture.Backend = config.CaptureBackendPortalPipewire

	backend, err := PrepareBackend(cfg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if backend != config.CaptureBackendPortalPipewire {
		t.Fatalf("expected backend=%q, got %q", config.CaptureBackendPortalPipewire, backend)
	}
}

func TestPrepareBackendLinuxRejectsUnsupported(t *testing.T) {
	cfg := config.ServerConfig{}
	cfg.Capture.Backend = config.CaptureBackendDDAGrab

	if _, err := PrepareBackend(cfg); err == nil {
		t.Fatalf("expected error for unsupported linux backend")
	}
}
