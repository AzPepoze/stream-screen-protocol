//go:build windows

package nvenc

import (
	"fmt"
	"sync"

	"streamscreen/internal/video/codec/h264/backends/rtpffmpeg"
)

type Encoder struct {
	fps     int
	preset  string
	tune    string
	bitrate int

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
	preset := stringFrom(cfg, "preset", "p1")
	tune := stringFrom(cfg, "tune", "ll")
	return &Encoder{fps: fps, preset: preset, tune: tune, bitrate: bitrate}, nil
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
		Codec:       "h264_nvenc",
		InputFormat: "bgra",
		FPS:         e.fps,
		Width:       width,
		Height:      height,
		ExtraArgs: []string{
			"-preset", e.preset,
			"-tune", e.tune,
			"-rc", "cbr",
			"-b:v", fmt.Sprintf("%dk", e.bitrate),
			"-maxrate", fmt.Sprintf("%dk", e.bitrate),
			"-bufsize", fmt.Sprintf("%dk", e.bitrate),
			"-g", fmt.Sprintf("%d", e.fps),
			"-bf", "0",
			"-forced-idr", "1",
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
		return nil, fmt.Errorf("nvenc encoder: invalid frame size got=%d expected=%d", len(rgbaData), width*height*4)
	}
	if err := e.ensureSession(width, height); err != nil {
		return nil, fmt.Errorf("nvenc encoder: persistent session: %w", err)
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
	if v, ok := cfg[key]; ok {
		if t, ok := v.(int); ok {
			return t
		}
		if t, ok := v.(float64); ok {
			return int(t)
		}
	}
	return fallback
}

func stringFrom(cfg map[string]interface{}, key, fallback string) string {
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}
