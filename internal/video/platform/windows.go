//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"time"

	"streamscreen/internal/config"
)

func prepareBackend(cfg config.ServerConfig) (config.CaptureBackend, error) {
	backend, err := cfg.EffectiveBackend()
	if err != nil {
		return "", err
	}

	if backend == config.CaptureBackendDDAGrab {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !ddagrabAvailable(ctx, cfg.Capture.FPS) {
			log.Printf("platform(windows): ddagrab probe failed, falling back to gdigrab")
			backend = config.CaptureBackendGDIGrab
		}
	}

	return backend, nil
}

func validateBackendRuntime(_ config.CaptureBackend) error {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("ffmpeg was not found in PATH")
	}
	return nil
}

func ddagrabAvailable(ctx context.Context, fps int) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", fmt.Sprintf("ddagrab=framerate=%d", fps),
		"-frames:v", "1",
		"-f", "null",
		"-",
	)
	cmd.Env = os.Environ()
	return cmd.Run() == nil
}
