//go:build windows

package nvenc

import (
	"fmt"
	"io"
	"os/exec"
)

type Encoder struct {
	preset  string
	tune    string
	bitrate int
}

func NewEncoder(cfg map[string]interface{}) (*Encoder, error) {
	fps := intFrom(cfg, "fps", 60)
	if fps <= 0 {
		fps = 60
	}
	_ = fps // Currently unused, preserved for future rate control logic
	bitrate := intFrom(cfg, "bitrate", 8000)
	if bitrate <= 0 {
		bitrate = 8000
	}
	preset := stringFrom(cfg, "preset", "p4") // NVENC presets p1 to p7, p4 is medium
	tune := stringFrom(cfg, "tune", "ll")    // ll=low latency

	return &Encoder{preset: preset, tune: tune, bitrate: bitrate}, nil
}

func (e *Encoder) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-f", "rawvideo",
		"-pix_fmt", "bgra",
		"-s", fmt.Sprintf("%dx%d", width, height),
		"-r", "60",
		"-i", "pipe:0",
		"-an",
		"-frames:v", "1",
		"-c:v", "h264_nvenc",
		"-preset", e.preset,
		"-tune", e.tune,
		"-rc", "cbr",
		"-b:v", fmt.Sprintf("%dk", e.bitrate),
		"-bf", "0",
		"-f", "h264",
		"pipe:1",
	}

	cmd := exec.Command("ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("nvenc encoder: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("nvenc encoder: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("nvenc encoder: start failed: %w", err)
	}

	if _, err := stdin.Write(rgbaData); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("nvenc encoder: stdin write: %w", err)
	}
	_ = stdin.Close()

	out, err := io.ReadAll(stdout)
	if err != nil {
		return nil, fmt.Errorf("nvenc encoder: stdout read: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("nvenc encoder: wait failed: %w", err)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("nvenc encoder: no output")
	}

	return out, nil
}

func (e *Encoder) Close() error { return nil }

func intFrom(cfg map[string]interface{}, key string, fallback int) int {
	if v, ok := cfg[key]; ok {
		if t, ok := v.(int); ok { return t }
		if t, ok := v.(float64); ok { return int(t) }
	}
	return fallback
}
func stringFrom(cfg map[string]interface{}, key, fallback string) string {
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok { return s }
	}
	return fallback
}
