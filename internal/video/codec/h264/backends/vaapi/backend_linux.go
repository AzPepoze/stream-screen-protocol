//go:build linux

package vaapi

import (
	"fmt"
	"sync"

	"streamscreen/internal/video/codec/h264/backends/rtpffmpeg"
)

type Encoder struct {
	fps     int
	bitrate int
	device  string

	mu      sync.Mutex
	session *rtpffmpeg.Session
	width   int
	height  int
}

func NewEncoder(cfg map[string]interface{}) (*Encoder, error) {
	fps := intFrom(cfg, "fps", 60)
	if fps <= 0 {
		fps = 60
	}
	bitrate := intFrom(cfg, "bitrate", 8000)
	if bitrate <= 0 {
		bitrate = intFrom(cfg, "bitrate_kbps", 8000)
	}
	device := stringFrom(cfg, "vaapi_device", "/dev/dri/renderD128")
	if device == "" {
		device = "/dev/dri/renderD128"
	}
	return &Encoder{fps: fps, bitrate: bitrate, device: device}, nil
}

func (e *Encoder) ensureSession(width, height int) error {
	if e.session != nil && e.width == width && e.height == height {
		return nil
	}
	if e.session != nil {
		_ = e.session.Close()
		e.session = nil
	}

	session, err := rtpffmpeg.New(rtpffmpeg.Config{
		Codec:       "h264_vaapi",
		InputFormat: "rgba",
		FPS:         e.fps,
		Width:       width,
		Height:      height,
		GlobalArgs:  []string{"-vaapi_device", e.device},
		ExtraArgs: []string{
			"-vf", "format=nv12,hwupload",
			"-b:v", fmt.Sprintf("%dk", e.bitrate),
			"-maxrate", fmt.Sprintf("%dk", e.bitrate),
			"-bufsize", fmt.Sprintf("%dk", e.bitrate),
			"-g", fmt.Sprintf("%d", e.fps),
			"-bf", "0",
		},
	})
	if err != nil {
		return err
	}
	e.session = session
	e.width = width
	e.height = height
	return nil
}

func (e *Encoder) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(rgbaData) != width*height*4 {
		return nil, fmt.Errorf("vaapi encoder: invalid RGBA frame size got=%d expected=%d", len(rgbaData), width*height*4)
	}
	if err := e.ensureSession(width, height); err != nil {
		return nil, fmt.Errorf("vaapi encoder: persistent session: %w", err)
	}
	return e.session.Encode(rgbaData)
}

func (e *Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != nil {
		err := e.session.Close()
		e.session = nil
		return err
	}
	return nil
}

func intFrom(cfg map[string]interface{}, key string, fallback int) int {
	if cfg == nil {
		return fallback
	}
	if v, ok := cfg[key]; ok {
		switch t := v.(type) {
		case int:
			return t
		case float64:
			return int(t)
		}
	}
	return fallback
}

func stringFrom(cfg map[string]interface{}, key, fallback string) string {
	if cfg == nil {
		return fallback
	}
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}
