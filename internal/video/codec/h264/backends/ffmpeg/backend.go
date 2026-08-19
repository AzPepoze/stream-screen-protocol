package ffmpeg

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
	keyInt  int

	mu      sync.Mutex
	session *rtpffmpeg.Session
	width   int
	height  int
}

type Decoder struct {
	session *rtpffmpeg.Decoder
}

func NewEncoder(cfg map[string]interface{}) (*Encoder, error) {
	fps := intFrom(cfg, "fps", 60)
	if fps <= 0 {
		fps = 60
	}
	bitrate := intFrom(cfg, "bitrate", 5000)
	if bitrate <= 0 {
		bitrate = intFrom(cfg, "bitrate_kbps", 5000)
	}
	keyInt := intFrom(cfg, "key-int-max", fps)
	if keyInt <= 0 {
		keyInt = intFrom(cfg, "key_int_max", fps)
	}
	if keyInt <= 0 {
		keyInt = fps
	}
	preset := stringFrom(cfg, "preset", "ultrafast")
	if preset == "" {
		preset = stringFrom(cfg, "speed_preset", "ultrafast")
	}
	if preset == "" {
		preset = "ultrafast"
	}
	tune := stringFrom(cfg, "tune", "zerolatency")
	if tune == "" {
		tune = "zerolatency"
	}

	return &Encoder{fps: fps, preset: preset, tune: tune, bitrate: bitrate, keyInt: keyInt}, nil
}

func (e *Encoder) ensureSession(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("ffmpeg encoder: invalid dimensions %dx%d", width, height)
	}
	if e.session != nil && e.width == width && e.height == height {
		return nil
	}
	if e.session != nil {
		_ = e.session.Close()
		e.session = nil
	}

	session, err := rtpffmpeg.New(rtpffmpeg.Config{
		Codec:       "libx264",
		InputFormat: "rgba",
		FPS:         e.fps,
		Width:       width,
		Height:      height,
		ExtraArgs: []string{
			"-preset", e.preset,
			"-tune", e.tune,
			"-b:v", fmt.Sprintf("%dk", e.bitrate),
			"-maxrate", fmt.Sprintf("%dk", e.bitrate),
			"-bufsize", fmt.Sprintf("%dk", e.bitrate*2),
			"-bf", "0",
			"-x264-params", fmt.Sprintf("aud=1:bframes=0:keyint=%d:min-keyint=%d:scenecut=0:repeat-headers=1", e.keyInt, e.keyInt),
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
		return nil, fmt.Errorf("ffmpeg encoder: invalid RGBA frame size got=%d expected=%d", len(rgbaData), width*height*4)
	}
	if err := e.ensureSession(width, height); err != nil {
		return nil, fmt.Errorf("ffmpeg encoder: persistent session: %w", err)
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

func NewDecoder(cfg map[string]interface{}) (*Decoder, error) {
	_ = cfg
	return &Decoder{session: rtpffmpeg.NewDecoder()}, nil
}

func (d *Decoder) Decode(encodedData []byte, width, height int) ([]byte, error) {
	if d.session == nil {
		return nil, fmt.Errorf("ffmpeg decoder: decoder is closed")
	}
	return d.session.Decode(encodedData, width, height)
}

func (d *Decoder) Close() error {
	if d.session == nil {
		return nil
	}
	err := d.session.Close()
	d.session = nil
	return err
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
