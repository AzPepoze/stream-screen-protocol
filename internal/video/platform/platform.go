package platform

import "streamscreen/internal/config"

func PrepareBackend(cfg config.ServerConfig) (config.CaptureBackend, error) {
	return prepareBackend(cfg)
}

func ValidateBackendRuntime(backend config.CaptureBackend) error {
	return validateBackendRuntime(backend)
}
