package playback

import "streamscreen/internal/config"

// Player consumes decoded PCM frames.
type Player interface {
	PlayPCM(pcm []byte) error
	Close() error
}

func New(cfg config.ClientConfig) (Player, error) {
	return newPlayer(cfg)
}
