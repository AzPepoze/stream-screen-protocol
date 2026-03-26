package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"streamscreen/internal/config"
)

func ProbeFFmpeg() error {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("ffmpeg was not found in PATH")
	}
	return nil
}

func MissingBackendHelp(backend config.CaptureBackend) string {
	switch backend {
	case config.CaptureBackendPortalPipewire:
		return "ffmpeg on this machine was built without the pipewire input device. Install an FFmpeg build with PipeWire support or switch to another backend in server.config.json."
	case config.CaptureBackendGDIGrab:
		return "ffmpeg on this machine was built without the gdigrab input device. Install a Windows FFmpeg build with desktop capture support."
	case config.CaptureBackendDDAGrab:
		return "ffmpeg on this machine cannot use ddagrab. Install a Windows FFmpeg build with ddagrab support or override the backend to gdigrab."
	default:
		return "the selected FFmpeg capture backend is not available on this machine."
	}
}

func commandEnv() []string {
	return os.Environ()
}

func Join(args []string) string {
	return strings.Join(args, " ")
}

func ServerArgs(cfg config.ServerConfig, backend config.CaptureBackend, destinationHost string) []string {
	bitrate := fmt.Sprintf("%dk", cfg.Video.BitrateKbps)
	bufsize := fmt.Sprintf("%dk", cfg.Video.BitrateKbps*2)
	udpURL := fmt.Sprintf("udp://%s:%d?pkt_size=1316", destinationHost, cfg.Port)

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "warning",
		"-stats_period", "1",
		"-progress", "pipe:2",
	}

	source := cfg.Capture.Source
	if source == "" {
		source = "default"
	}

	switch backend {
	case config.CaptureBackendPortalPipewire:
		input := source
		if input == "default" {
			input = "0"
		}
		args = append(args,
			"-f", "pipewire",
			"-framerate", fmt.Sprintf("%d", cfg.Capture.FPS),
			"-i", input,
		)
	case config.CaptureBackendDDAGrab:
		filter := fmt.Sprintf("ddagrab=framerate=%d", cfg.Capture.FPS)
		if source != "default" {
			filter = fmt.Sprintf("ddagrab=output_idx=%s:framerate=%d", source, cfg.Capture.FPS)
		}
		args = append(args, "-f", "lavfi", "-i", filter)
	case config.CaptureBackendGDIGrab:
		input := source
		if input == "" || input == "default" {
			input = "desktop"
		}
		args = append(args,
			"-f", "gdigrab",
			"-framerate", fmt.Sprintf("%d", cfg.Capture.FPS),
			"-i", input,
		)
	}

	args = append(args,
		"-an",
		"-c:v", cfg.Video.Codec,
		"-preset", cfg.Video.Preset,
		"-tune", cfg.Video.Tune,
		"-pix_fmt", "yuv420p",
		"-b:v", bitrate,
		"-maxrate", bitrate,
		"-bufsize", bufsize,
		"-f", "mpegts",
		udpURL,
	)

	return args
}

func DdagrabAvailable(ctx context.Context, fps int) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "lavfi",
		"-i", fmt.Sprintf("ddagrab=framerate=%d", fps),
		"-frames:v", "1",
		"-f", "null",
		"-",
	)
	cmd.Env = commandEnv()
	return cmd.Run() == nil
}
