package capture

import (
	"context"

	"streamscreen/internal/config"
)

// Source provides RGBA frames from a platform-specific capture backend.
type Source interface {
	Start(ctx context.Context) error
	Frames() <-chan []byte
	Close() error
}

func New(cfg config.ServerConfig, backend config.CaptureBackend) (Source, error) {
	return newSource(cfg, backend)
}
