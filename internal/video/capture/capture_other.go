//go:build !linux && !windows

package capture

import (
	"fmt"

	"streamscreen/internal/config"
)

func newSource(_ config.ServerConfig, _ config.CaptureBackend) (Source, error) {
	return nil, fmt.Errorf("capture is unsupported on this operating system")
}
