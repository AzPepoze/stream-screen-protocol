//go:build !windows || !cgo

package wma

import (
	"fmt"

	"streamscreen/internal/config"
)

type Encoder struct{}
type Decoder struct{}

func NewEncoder(_ config.ServerConfig) (*Encoder, error) {
	return nil, fmt.Errorf("WMA audio encoder requires Windows with cgo support")
}

func NewDecoder(_ config.ClientConfig) (*Decoder, error) {
	return nil, fmt.Errorf("WMA audio decoder requires Windows with cgo support")
}

func encodeWMA(_ []byte, _, _, _ int) ([]byte, error) {
	return nil, fmt.Errorf("WMA encoding unavailable: requires Windows with cgo")
}

func decodeWMA(_ []byte, _, _ int) ([]byte, error) {
	return nil, fmt.Errorf("WMA decoding unavailable: requires Windows with cgo")
}

func (e *Encoder) EncodePCM(_ []byte) ([]byte, error) {
	return nil, fmt.Errorf("WMA encoding unavailable: requires Windows with cgo")
}

func (d *Decoder) DecodeToPCM(_ []byte) ([]byte, error) {
	return nil, fmt.Errorf("WMA decoding unavailable: requires Windows with cgo")
}

func (d *Decoder) SetFormat(_, _ int) {}

func (e *Encoder) Close() error { return nil }

func (d *Decoder) Close() error { return nil }
