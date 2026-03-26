//go:build !linux

package portal

import (
	"context"
	"errors"
	"os"

	"streamscreen/internal/config"
)

type StreamInfo struct {
	NodeID     uint32
	Properties map[string]any
}

type ScreenCastSession struct {
	Streams []StreamInfo
}

func (s *ScreenCastSession) RemoteFile() *os.File {
	return nil
}

func (s *ScreenCastSession) Close() error {
	return nil
}

func StartScreenCast(context.Context, config.ServerConfig) (*ScreenCastSession, error) {
	return nil, errors.New("portal screencast is only supported on linux")
}
