//go:build linux

package vaapi

import (
	"fmt"
	"io"
	"os/exec"
)

type Encoder struct {
	cmd *exec.Cmd
}

func NewEncoder(cfg map[string]interface{}) (*Encoder, error) {
	return &Encoder{}, nil
}

func (e *Encoder) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-vaapi_device", "/dev/dri/renderD128",
		"-f", "rawvideo",
		"-pix_fmt", "rgba", // PipeWire pipeline converts to RGBA
		"-s", fmt.Sprintf("%dx%d", width, height),
		"-r", "60",
		"-i", "pipe:0",
		"-vf", "format=nv12,hwupload",
		"-an",
		"-frames:v", "1",
		"-c:v", "h264_vaapi",
		"-b:v", "8000k",
		"-bf", "0",
		"-f", "h264",
		"pipe:1",
	}

	cmd := exec.Command("ffmpeg", args...)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()
	stdin.Write(rgbaData)
	stdin.Close()
	out, _ := io.ReadAll(stdout)
	cmd.Wait()
	return out, nil
}

func (e *Encoder) Close() error { return nil }
