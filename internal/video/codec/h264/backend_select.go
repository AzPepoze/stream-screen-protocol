package h264

import (
	"fmt"

	ffmpegbackend "streamscreen/internal/video/codec/h264/backends/ffmpeg"
	gstreamerbackend "streamscreen/internal/video/codec/h264/backends/gstreamer"
)

func NewEncoder(cfg Config) (Encoder, error) {
	backend, err := encoderBackendFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	switch backend {
	case BackendGStreamer:
		enc, err := gstreamerbackend.NewEncoder(cfg)
		if err != nil {
			return nil, fmt.Errorf("gstreamer encoder init failed: %w", err)
		}
		return enc, nil
	case BackendFFmpeg:
		enc, err := ffmpegbackend.NewEncoder(cfg)
		if err != nil {
			return nil, fmt.Errorf("ffmpeg encoder init failed: %w", err)
		}
		return enc, nil
	default:
		return nil, fmt.Errorf("unsupported h264 encoder backend: %s", backend)
	}
}

func NewDecoder(cfg Config) (Decoder, error) {
	backend, err := decoderBackendFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	switch backend {
	case BackendGStreamer:
		dec, err := gstreamerbackend.NewDecoder(cfg)
		if err != nil {
			return nil, fmt.Errorf("gstreamer decoder init failed: %w", err)
		}
		return dec, nil
	case BackendFFmpeg:
		dec, err := ffmpegbackend.NewDecoder(cfg)
		if err != nil {
			return nil, fmt.Errorf("ffmpeg decoder init failed: %w", err)
		}
		return dec, nil
	default:
		return nil, fmt.Errorf("unsupported h264 decoder backend: %s", backend)
	}
}
