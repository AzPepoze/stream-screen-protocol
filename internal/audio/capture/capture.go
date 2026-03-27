package capture

import (
	"context"

	"streamscreen/internal/config"
)

// Source provides raw PCM frames (S16LE interleaved).
type Source interface {
	Start(ctx context.Context) error
	Frames() <-chan []byte
	Close() error
}

func New(cfg config.ServerConfig) (Source, error) {
	return newSource(cfg)
}
