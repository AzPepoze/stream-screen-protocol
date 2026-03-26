//go:build windows

package capture

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"

	"streamscreen/internal/config"
)

type windowsSource struct {
	cfg     config.ServerConfig
	backend config.CaptureBackend
	frames  chan []byte
	cmd     *exec.Cmd
	cancel  context.CancelFunc
}

func newSource(cfg config.ServerConfig, backend config.CaptureBackend) (Source, error) {
	switch backend {
	case config.CaptureBackendDDAGrab, config.CaptureBackendGDIGrab:
		return &windowsSource{cfg: cfg, backend: backend, frames: make(chan []byte, 8)}, nil
	default:
		return nil, fmt.Errorf("windows capture backend %q is not supported by internal capture", backend)
	}
}

func (s *windowsSource) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	args := s.rawRGBAArgs()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd

	go s.consumeFrames(ctx, stdout)
	go s.consumeStderr(stderr)
	go func() {
		_ = cmd.Wait()
		cancel()
	}()

	return nil
}

func (s *windowsSource) Frames() <-chan []byte {
	return s.frames
}

func (s *windowsSource) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}

func (s *windowsSource) consumeFrames(ctx context.Context, r io.Reader) {
	frameSize := s.cfg.Capture.Width * s.cfg.Capture.Height * 4
	if frameSize <= 0 {
		return
	}
	buf := make([]byte, frameSize)

	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		frame := append([]byte(nil), buf...)

		select {
		case <-ctx.Done():
			return
		case s.frames <- frame:
		default:
			select {
			case <-s.frames:
			default:
			}
			select {
			case <-ctx.Done():
				return
			case s.frames <- frame:
			default:
			}
		}
	}
}

func (s *windowsSource) consumeStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		_ = scanner.Text()
	}
}

func (s *windowsSource) rawRGBAArgs() []string {
	source := s.cfg.Capture.Source
	if source == "" {
		source = "default"
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "warning",
	}

	switch s.backend {
	case config.CaptureBackendDDAGrab:
		filter := fmt.Sprintf("ddagrab=framerate=%d", s.cfg.Capture.FPS)
		if source != "default" {
			filter = fmt.Sprintf("ddagrab=output_idx=%s:framerate=%d", source, s.cfg.Capture.FPS)
		}
		args = append(args, "-f", "lavfi", "-i", filter)
	case config.CaptureBackendGDIGrab:
		input := source
		if input == "" || input == "default" {
			input = "desktop"
		}
		args = append(args,
			"-f", "gdigrab",
			"-framerate", fmt.Sprintf("%d", s.cfg.Capture.FPS),
			"-i", input,
		)
	}

	args = append(args,
		"-an",
		"-vf", fmt.Sprintf("scale=%d:%d", s.cfg.Capture.Width, s.cfg.Capture.Height),
		"-pix_fmt", "rgba",
		"-f", "rawvideo",
		"pipe:1",
	)
	return args
}
