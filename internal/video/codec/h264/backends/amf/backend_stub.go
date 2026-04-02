//go:build !windows

package amf

import "fmt"

type Encoder struct{}
func NewEncoder(cfg map[string]interface{}) (*Encoder, error) {
	return nil, fmt.Errorf("amf is only supported on Windows")
}
func (e *Encoder) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (e *Encoder) Close() error { return nil }
