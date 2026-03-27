//go:build !linux && !windows

package capture

import (
	"fmt"

	"streamscreen/internal/config"
)

func newSource(_ config.ServerConfig) (Source, error) {
	return nil, fmt.Errorf("audio capture is not supported on this platform")
}
