//go:build !linux && !windows

package platform

import (
	"fmt"

	"streamscreen/internal/config"
)

func prepareBackend(cfg config.ServerConfig) (config.CaptureBackend, error) {
	return "", fmt.Errorf("unsupported operating system")
}

func validateBackendRuntime(_ config.CaptureBackend) error {
	return fmt.Errorf("unsupported operating system")
}
