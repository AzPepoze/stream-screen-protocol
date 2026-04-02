//go:build windows

package amf

import (
	"fmt"
	"io"
	"os/exec"
)

type Encoder struct {
}

func NewEncoder(cfg map[string]interface{}) (*Encoder, error) {
	return &Encoder{}, nil
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
		"-c:v", "h264_amf",
		"-quality", "speed", // Low latency
		"-rc", "cbr",
		"-b:v", "8000k",
		"-f", "h264",
		"pipe:1",
	}

	cmd := exec.Command("ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("amf encoder: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("amf encoder: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("amf encoder: start failed: %w", err)
	}

	if _, err := stdin.Write(rgbaData); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("amf encoder: stdin write: %w", err)
	}
	_ = stdin.Close()

	out, err := io.ReadAll(stdout)
	if err != nil {
		return nil, fmt.Errorf("amf encoder: stdout read: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("amf encoder: wait failed: %w", err)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("amf encoder: no output")
	}

	return out, nil
}

func (e *Encoder) Close() error { return nil }
