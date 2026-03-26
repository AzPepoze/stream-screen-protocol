//go:build !linux

package server

import "fmt"

func (s *Sender) Start(_ int, _ uint32) error {
	return fmt.Errorf("legacy pipewire capture start is only supported on linux")
}
