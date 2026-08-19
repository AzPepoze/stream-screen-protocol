package rtpffmpeg

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Decoder keeps one FFmpeg decoder process alive. Raw output is fixed-size
// RGBA, so unlike encoded H.264 it can be framed deterministically with
// io.ReadFull without restarting FFmpeg for each frame.
type Decoder struct {
	mu sync.Mutex

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	frames chan []byte
	errCh  chan error
	done   chan struct{}

	width  int
	height int
	closed bool
}

func NewDecoder() *Decoder {
	return &Decoder{}
}

func (d *Decoder) start(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("rtpffmpeg decoder: invalid dimensions %dx%d", width, height)
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-f", "h264",
		"-i", "pipe:0",
		"-an",
		"-f", "rawvideo",
		"-pix_fmt", "rgba",
		"pipe:1",
	}
	cmd := exec.Command("ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("rtpffmpeg decoder: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("rtpffmpeg decoder: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("rtpffmpeg decoder: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("rtpffmpeg decoder: start FFmpeg: %w", err)
	}

	d.cmd = cmd
	d.stdin = stdin
	d.stdout = stdout
	d.width = width
	d.height = height
	d.frames = make(chan []byte, 4)
	d.errCh = make(chan error, 1)
	d.done = make(chan struct{})

	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	go d.readFrames(width * height * 4)
	go func() {
		err := cmd.Wait()
		select {
		case <-d.done:
			return
		default:
		}
		if err == nil {
			err = errors.New("FFmpeg decoder exited unexpectedly")
		}
		select {
		case d.errCh <- fmt.Errorf("rtpffmpeg decoder: %w", err):
		default:
		}
	}()
	return nil
}

func (d *Decoder) readFrames(frameSize int) {
	for {
		frame := make([]byte, frameSize)
		if _, err := io.ReadFull(d.stdout, frame); err != nil {
			select {
			case <-d.done:
				return
			default:
			}
			select {
			case d.errCh <- fmt.Errorf("rtpffmpeg decoder: read frame: %w", err):
			default:
			}
			return
		}
		select {
		case d.frames <- frame:
		case <-d.done:
			return
		}
	}
}

func (d *Decoder) Decode(accessUnit []byte, width, height int) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil, errors.New("rtpffmpeg decoder: decoder is closed")
	}
	if len(accessUnit) == 0 {
		return nil, errors.New("rtpffmpeg decoder: empty H.264 access unit")
	}
	if d.cmd == nil || width != d.width || height != d.height {
		d.stopLocked()
		if err := d.start(width, height); err != nil {
			return nil, err
		}
	}

	if _, err := d.stdin.Write(accessUnit); err != nil {
		return nil, fmt.Errorf("rtpffmpeg decoder: write access unit: %w", err)
	}

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case frame := <-d.frames:
		return frame, nil
	case err := <-d.errCh:
		return nil, err
	case <-timer.C:
		return nil, errors.New("rtpffmpeg decoder: timed out waiting for decoded frame")
	case <-d.done:
		return nil, errors.New("rtpffmpeg decoder: decoder stopped")
	}
}

func (d *Decoder) stopLocked() {
	if d.done != nil {
		select {
		case <-d.done:
		default:
			close(d.done)
		}
	}
	if d.stdin != nil {
		_ = d.stdin.Close()
	}
	if d.stdout != nil {
		_ = d.stdout.Close()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
	}
	d.cmd = nil
	d.stdin = nil
	d.stdout = nil
	d.frames = nil
	d.errCh = nil
	d.done = nil
	d.width = 0
	d.height = 0
}

func (d *Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	d.stopLocked()
	return nil
}
