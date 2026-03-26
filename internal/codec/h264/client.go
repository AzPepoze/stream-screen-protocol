package h264

import (
	"fmt"
	"streamscreen/internal/codec"
)

// ClientPipeline handles H264 decoding
type ClientPipeline struct {
	decoder codec.Decoder
	config  codec.Config
}

// NewClientPipeline creates a new H264 client pipeline
func NewClientPipeline(cfg codec.Config) (*ClientPipeline, error) {
	h264Dec, err := codec.NewDecoder("h264", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create h264 decoder: %w", err)
	}
	return &ClientPipeline{
		decoder: h264Dec,
		config:  cfg,
	}, nil
}

// HandleFrame decodes H264 data to RGBA
func (p *ClientPipeline) HandleFrame(h264Data []byte, width, height int) ([]byte, error) {
	if p.decoder == nil {
		return nil, fmt.Errorf("decoder not initialized")
	}

	if width == 0 || height == 0 {
		return nil, fmt.Errorf("video dimensions not set")
	}

	// Decode H264 frame to RGBA
	rgbaData, err := p.decoder.Decode(h264Data, width, height)
	if err != nil {
		return nil, err
	}

	return rgbaData, nil
}

// Close stops the decoder
func (p *ClientPipeline) Close() error {
	if p.decoder != nil {
		return p.decoder.Close()
	}
	return nil
}
