//go:build windows

package capture

import (
	"fmt"
	"os/exec"

	"streamscreen/internal/config"
)

func newSource(cfg config.ServerConfig) (Source, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("audio capture requires ffmpeg in PATH: %w", err)
	}
	rate := cfg.Audio.SampleRate
	if rate <= 0 {
		rate = 48000
	}
	ch := cfg.Audio.Channels
	if ch <= 0 {
		ch = 2
	}
	device := audioInputDevice(cfg, "audio=virtual-audio-capturer")

	return newFFmpegSource(cfg, func() *exec.Cmd {
		args := []string{
			"-hide_banner", "-nostdin", "-loglevel", "error",
			"-fflags", "nobuffer",
			"-flags", "low_delay",
			"-f", "dshow",
			"-i", device,
			"-ac", fmt.Sprintf("%d", ch),
			"-ar", fmt.Sprintf("%d", rate),
			"-f", "s16le",
			"pipe:1",
		}
		return exec.Command("ffmpeg", args...)
	})
}
