//go:build !cgo

package opus

import (
	"fmt"

	"streamscreen/internal/config"
)

type Encoder struct{}
type Decoder struct{}

func NewEncoder(_ config.ServerConfig) (*Encoder, error) {
	return nil, fmt.Errorf("audio opus encoder requires cgo build (hraban/opus backend)")
}

func NewDecoder(_ config.ClientConfig) (*Decoder, error) {
	return nil, fmt.Errorf("audio opus decoder requires cgo build (hraban/opus backend)")
}

func (e *Encoder) EncodePCM(_ []byte) ([]byte, error) {
	return nil, fmt.Errorf("audio opus encoder unavailable without cgo")
}

func (d *Decoder) DecodeToPCM(_ []byte) ([]byte, error) {
	return nil, fmt.Errorf("audio opus decoder unavailable without cgo")
}

func (d *Decoder) SetFormat(_, _ int) {}

func (e *Encoder) Close() error { return nil }

func (d *Decoder) Close() error { return nil }
