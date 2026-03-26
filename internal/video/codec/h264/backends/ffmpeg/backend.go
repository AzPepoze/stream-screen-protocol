package ffmpeg

import (
	"fmt"
	"io"
	"os/exec"
)

type Encoder struct {
	fps     int
	preset  string
	tune    string
	bitrate int
	keyInt  int
}

type Decoder struct {
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

func (e *Encoder) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("ffmpeg encoder: invalid dimensions %dx%d", width, height)
	}
	if len(rgbaData) != width*height*4 {
		return nil, fmt.Errorf("ffmpeg encoder: invalid RGBA frame size got=%d expected=%d", len(rgbaData), width*height*4)
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-f", "rawvideo",
		"-pix_fmt", "rgba",
		"-s", fmt.Sprintf("%dx%d", width, height),
		"-r", fmt.Sprintf("%d", e.fps),
		"-i", "pipe:0",
		"-an",
		"-frames:v", "1",
		"-c:v", "libx264",
		"-preset", e.preset,
		"-tune", e.tune,
		"-b:v", fmt.Sprintf("%dk", e.bitrate),
		"-maxrate", fmt.Sprintf("%dk", e.bitrate),
		"-bufsize", fmt.Sprintf("%dk", e.bitrate*2),
		"-x264-params", fmt.Sprintf("aud=1:bframes=0:keyint=%d:min-keyint=%d:scenecut=0:repeat-headers=1", e.keyInt, e.keyInt),
		"-f", "h264",
		"pipe:1",
	}

	cmd := exec.Command("ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg encoder stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg encoder stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg encoder stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg encoder start failed: %w", err)
	}

	if _, err := stdin.Write(rgbaData); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, fmt.Errorf("ffmpeg encoder stdin write failed: %w", err)
	}
	_ = stdin.Close()

	out, readErr := io.ReadAll(stdout)
	errOut, _ := io.ReadAll(stderr)
	waitErr := cmd.Wait()

	if readErr != nil {
		return nil, fmt.Errorf("ffmpeg encoder stdout read failed: %w", readErr)
	}
	if waitErr != nil {
		if len(errOut) > 0 {
			return nil, fmt.Errorf("ffmpeg encoder failed: %s", string(errOut))
		}
		return nil, fmt.Errorf("ffmpeg encoder failed: %w", waitErr)
	}
	if len(out) == 0 {
		if len(errOut) > 0 {
			return nil, fmt.Errorf("ffmpeg encoder produced no output: %s", string(errOut))
		}
		return nil, fmt.Errorf("ffmpeg encoder produced no output")
	}

	return out, nil
}

func (e *Encoder) Close() error { return nil }

func NewDecoder(cfg map[string]interface{}) (*Decoder, error) {
	_ = cfg
	return &Decoder{}, nil
}

func (d *Decoder) Decode(encodedData []byte, width, height int) ([]byte, error) {
	if len(encodedData) == 0 {
		return nil, fmt.Errorf("ffmpeg decoder: empty encoded data")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("ffmpeg decoder: invalid dimensions %dx%d", width, height)
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-f", "h264",
		"-i", "pipe:0",
		"-an",
		"-frames:v", "1",
		"-f", "rawvideo",
		"-pix_fmt", "rgba",
		"pipe:1",
	}

	cmd := exec.Command("ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decoder stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decoder stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decoder stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg decoder start failed: %w", err)
	}

	if _, err := stdin.Write(encodedData); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, fmt.Errorf("ffmpeg decoder stdin write failed: %w", err)
	}
	_ = stdin.Close()

	out, readErr := io.ReadAll(stdout)
	errOut, _ := io.ReadAll(stderr)
	waitErr := cmd.Wait()

	if readErr != nil {
		return nil, fmt.Errorf("ffmpeg decoder stdout read failed: %w", readErr)
	}
	if waitErr != nil {
		if len(errOut) > 0 {
			return nil, fmt.Errorf("ffmpeg decoder failed: %s", string(errOut))
		}
		return nil, fmt.Errorf("ffmpeg decoder failed: %w", waitErr)
	}

	expected := width * height * 4
	if len(out) != expected {
		return nil, fmt.Errorf("ffmpeg decoder: invalid output size got=%d expected=%d", len(out), expected)
	}

	return out, nil
}

func (d *Decoder) Close() error { return nil }

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
