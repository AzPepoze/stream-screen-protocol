//go:build !linux

package gstreamer

import "fmt"

type Encoder struct{}

type Decoder struct{}

func NewEncoder(_ map[string]interface{}) (*Encoder, error) {
	return nil, fmt.Errorf("gstreamer h264 encoder backend is only available on linux")
}

func NewDecoder(_ map[string]interface{}) (*Decoder, error) {
	return nil, fmt.Errorf("gstreamer h264 decoder backend is only available on linux")
}

func (e *Encoder) Encode(_ []byte, _, _ int) ([]byte, error) {
	return nil, fmt.Errorf("gstreamer h264 encoder backend is unavailable")
}

func (e *Encoder) Close() error { return nil }

func (d *Decoder) Decode(_ []byte, _, _ int) ([]byte, error) {
	return nil, fmt.Errorf("gstreamer h264 decoder backend is unavailable")
}

func (d *Decoder) Close() error { return nil }
