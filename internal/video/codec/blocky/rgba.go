package blocky

import (
	"fmt"
	"streamscreen/internal/codec"
)

// RGBA is a pass-through codec that does no encoding/decoding
// Uses raw RGBA format (4 bytes per pixel)
type RGBA struct{}

// NewRGBA creates a new RGBA codec
func NewRGBA() *RGBA {
	return &RGBA{}
}

// Encode returns the RGBA data as-is (no compression)
func (r *RGBA) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	if len(rgbaData) != width*height*4 {
		return nil, fmt.Errorf("rgba: invalid frame size: got %d, expected %d", len(rgbaData), width*height*4)
	}
	// Return as-is, avoiding copy
	return rgbaData, nil
}

// Decode returns the encoded data as RGBA (no decompression)
func (r *RGBA) Decode(encodedData []byte, width, height int) ([]byte, error) {
	expectedSize := width * height * 4
	if len(encodedData) != expectedSize {
		return nil, fmt.Errorf("rgba: invalid decoded frame size: got %d, expected %d", len(encodedData), expectedSize)
	}
	// Return as-is, no decoding needed
	return encodedData, nil
}

// CodecName returns "blocky"
func (r *RGBA) CodecName() string {
	return "blocky"
}

// Close is a no-op for blocky codec
func (r *RGBA) Close() error {
	return nil
}

// init registers blocky codec factories with the codec package
func init() {
	codec.RegisterEncoder("blocky", func(cfg codec.Config) (codec.Encoder, error) {
		return NewRGBA(), nil
	})
	codec.RegisterDecoder("blocky", func(cfg codec.Config) (codec.Decoder, error) {
		return NewRGBA(), nil
	})
}
