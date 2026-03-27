package capture

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"streamscreen/internal/config"
)

type ffmpegSource struct {
	cfg           config.ServerConfig
	frames        chan []byte
	frameBytes    int
	startCmd      func() *exec.Cmd
	cancel        context.CancelFunc
	closeOnce     sync.Once
	cmdMu         sync.Mutex
	cmd           *exec.Cmd
	stdout        io.ReadCloser
	readErrLogged bool
}

func newFFmpegSource(cfg config.ServerConfig, startCmd func() *exec.Cmd) (Source, error) {
	sampleRate := cfg.Audio.SampleRate
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	channels := cfg.Audio.Channels
	if channels <= 0 {
		channels = 2
	}
	frameMS := cfg.Audio.FrameMS
	if frameMS <= 0 {
		frameMS = 20
	}

	frameBytes := (sampleRate * channels * 2 * frameMS) / 1000
	if frameBytes <= 0 {
		return nil, fmt.Errorf("invalid audio frame size")
	}

	return &ffmpegSource{
		cfg:        cfg,
		frames:     make(chan []byte, 32),
		frameBytes: frameBytes,
		startCmd:   startCmd,
	}, nil
}

func (s *ffmpegSource) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	cmd := s.startCmd()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("audio capture stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("audio capture start failed: %w", err)
	}

	s.cmdMu.Lock()
	s.cmd = cmd
	s.stdout = stdout
	s.cmdMu.Unlock()

	go s.readLoop(runCtx)
	go s.waitLoop()
	return nil
}

func (s *ffmpegSource) Frames() <-chan []byte {
	return s.frames
}

func (s *ffmpegSource) Close() error {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.cmdMu.Lock()
		cmd := s.cmd
		stdout := s.stdout
		s.cmd = nil
		s.stdout = nil
		s.cmdMu.Unlock()
		if stdout != nil {
			_ = stdout.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	return nil
}

func (s *ffmpegSource) readLoop(ctx context.Context) {
	defer close(s.frames)

	buf := make([]byte, s.frameBytes)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := io.ReadFull(s.stdout, buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			return
		}
		if n != s.frameBytes {
			continue
		}

		frame := make([]byte, s.frameBytes)
		copy(frame, buf)

		select {
		case s.frames <- frame:
		default:
			// Realtime audio policy: drop oldest frame first.
			select {
			case <-s.frames:
			default:
			}
			select {
			case s.frames <- frame:
			default:
			}
		}
	}
}

func (s *ffmpegSource) waitLoop() {
	s.cmdMu.Lock()
	cmd := s.cmd
	s.cmdMu.Unlock()
	if cmd == nil {
		return
	}
	_ = cmd.Wait()
	// Give reader a moment to flush/exit gracefully.
	time.Sleep(10 * time.Millisecond)
}

func audioInputDevice(cfg config.ServerConfig, fallback string) string {
	if cfg.Audio.InputDevice != "" {
		return cfg.Audio.InputDevice
	}
	if cfg.CodecConfig != nil {
		if v, ok := cfg.CodecConfig["audio_input_device"]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return fallback
}
