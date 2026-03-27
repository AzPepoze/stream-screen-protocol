//go:build !linux && !windows

package playback

import (
	"fmt"

	"streamscreen/internal/config"
)

func newPlayer(_ config.ClientConfig) (Player, error) {
	return nil, fmt.Errorf("audio playback is not supported on this platform")
}
