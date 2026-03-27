//go:build (linux || windows) && !cgo

package playback

import (
	"fmt"

	"streamscreen/internal/config"
)

func newPlayer(_ config.ClientConfig) (Player, error) {
	return nil, fmt.Errorf("audio playback on linux/windows requires cgo-enabled build (miniaudio backend)")
}
