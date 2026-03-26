package h264

import "fmt"

// ServerPipeline handles H264 encoding and packet fragmentation.
type ServerPipeline struct {
	encoder Encoder
	config  Config
}

func NewServerPipeline(cfg Config) (*ServerPipeline, error) {
	enc, err := NewEncoder(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create h264 encoder: %w", err)
	}
	return &ServerPipeline{encoder: enc, config: cfg}, nil
}

func (p *ServerPipeline) SendFrame(frameData []byte, width, height int) ([]byte, error) {
	if p.encoder == nil {
		return nil, fmt.Errorf("encoder not initialized")
	}
	return p.encoder.Encode(frameData, width, height)
}

func (p *ServerPipeline) Close() error {
	if p.encoder != nil {
		return p.encoder.Close()
	}
	return nil
}

// ClientPipeline handles H264 decoding.
type ClientPipeline struct {
	decoder Decoder
	config  Config
}

func NewClientPipeline(cfg Config) (*ClientPipeline, error) {
	dec, err := NewDecoder(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create h264 decoder: %w", err)
	}
	return &ClientPipeline{decoder: dec, config: cfg}, nil
}

func (p *ClientPipeline) HandleFrame(h264Data []byte, width, height int) ([]byte, error) {
	if p.decoder == nil {
		return nil, fmt.Errorf("decoder not initialized")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("video dimensions not set")
	}
	return p.decoder.Decode(h264Data, width, height)
}

func (p *ClientPipeline) Close() error {
	if p.decoder != nil {
		return p.decoder.Close()
	}
	return nil
}
