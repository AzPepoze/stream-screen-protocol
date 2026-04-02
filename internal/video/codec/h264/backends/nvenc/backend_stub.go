//go:build !windows

package nvenc

import "fmt"

type Encoder struct{}
func NewEncoder(cfg map[string]interface{}) (*Encoder, error) {
	return nil, fmt.Errorf("nvenc is only supported on Windows in this implementation")
}
func (e *Encoder) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (e *Encoder) Close() error { return nil }
