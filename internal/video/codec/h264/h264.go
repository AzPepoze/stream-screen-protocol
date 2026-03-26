package h264

import (
	"fmt"
	"runtime"
)

const (
	BackendAuto      = "auto"
	BackendGStreamer = "gstreamer"
	BackendFFmpeg    = "ffmpeg"
)

type Config map[string]interface{}

type Encoder interface {
	Encode(rgbaData []byte, width, height int) ([]byte, error)
	Close() error
}

type Decoder interface {
	Decode(encodedData []byte, width, height int) ([]byte, error)
	Close() error
}

func (c Config) GetString(key, fallback string) string {
	if v, ok := c[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

func (c Config) GetInt(key string, fallback int) int {
	if v, ok := c[key]; ok {
		switch t := v.(type) {
		case int:
			return t
		case float64:
			return int(t)
		}
	}
	return fallback
}

func normalizeBackend(v string) string {
	switch v {
	case "", BackendAuto:
		return BackendAuto
	case BackendGStreamer:
		return BackendGStreamer
	case BackendFFmpeg:
		return BackendFFmpeg
	default:
		return ""
	}
}

func defaultEncoderBackend() string {
	if runtime.GOOS == "windows" {
		return BackendFFmpeg
	}
	return BackendGStreamer
}

func defaultDecoderBackend() string {
	if runtime.GOOS == "windows" {
		return BackendFFmpeg
	}
	return BackendGStreamer
}

func encoderBackendFromConfig(cfg Config) (string, error) {
	selected := normalizeBackend(cfg.GetString("h264_encoder_backend", BackendAuto))
	if selected == "" {
		return "", fmt.Errorf("invalid h264_encoder_backend, expected auto|gstreamer|ffmpeg")
	}
	if selected == BackendAuto {
		selected = defaultEncoderBackend()
	}
	return selected, nil
}

func decoderBackendFromConfig(cfg Config) (string, error) {
	selected := normalizeBackend(cfg.GetString("h264_decoder_backend", BackendAuto))
	if selected == "" {
		return "", fmt.Errorf("invalid h264_decoder_backend, expected auto|gstreamer|ffmpeg")
	}
	if selected == BackendAuto {
		selected = defaultDecoderBackend()
	}
	return selected, nil
}
