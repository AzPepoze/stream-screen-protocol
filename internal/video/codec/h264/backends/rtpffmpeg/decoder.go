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

var h264AccessUnitBoundary = []byte{0x00, 0x00, 0x00, 0x01, 0x09, 0xf0}

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
		"-probesize", "32",
		"-analyzeduration", "0",
		"-f", "h264",
		"-i", "pipe:0",
		"-an",
		"-f", "rawvideo",
		"-pix_fmt", "rgba",
		"-flush_packets", "1",
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

	frames := make(chan []byte, 4)
	errCh := make(chan error, 1)
	done := make(chan struct{})

	d.cmd = cmd
	d.stdin = stdin
	d.stdout = stdout
	d.width = width
	d.height = height
	d.frames = frames
	d.errCh = errCh
	d.done = done

	go func(stderr io.Reader) {
		_, _ = io.Copy(io.Discard, stderr)
	}(stderr)
	go readDecodedFrames(stdout, width*height*4, frames, errCh, done)
	go waitDecoderProcess(cmd, errCh, done)
	return nil
}

func readDecodedFrames(stdout io.Reader, frameSize int, frames chan<- []byte, errCh chan<- error, done <-chan struct{}) {
	for {
		frame := make([]byte, frameSize)
		if _, err := io.ReadFull(stdout, frame); err != nil {
			select {
			case <-done:
				return
			default:
			}
			select {
			case errCh <- fmt.Errorf("rtpffmpeg decoder: read frame: %w", err):
			default:
			}
			return
		}
		select {
		case frames <- frame:
		case <-done:
			return
		}
	}
}

func waitDecoderProcess(cmd *exec.Cmd, errCh chan<- error, done <-chan struct{}) {
	err := cmd.Wait()
	select {
	case <-done:
		return
	default:
	}
	if err == nil {
		err = errors.New("FFmpeg decoder exited unexpectedly")
	}
	select {
	case errCh <- fmt.Errorf("rtpffmpeg decoder: %w", err):
	default:
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

	stdin := d.stdin
	frames := d.frames
	errCh := d.errCh
	done := d.done
	if _, err := stdin.Write(accessUnit); err != nil {
		return nil, fmt.Errorf("rtpffmpeg decoder: write access unit: %w", err)
	}
	// The raw H.264 parser normally finalizes a picture when it sees the start
	// of the following access unit. Since Decode is synchronous and the FFmpeg
	// process stays alive, append a standalone AUD delimiter after each unit so
	// the current picture can be emitted immediately without closing stdin.
	if _, err := stdin.Write(h264AccessUnitBoundary); err != nil {
		return nil, fmt.Errorf("rtpffmpeg decoder: write access-unit boundary: %w", err)
	}

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case frame := <-frames:
		return frame, nil
	case err := <-errCh:
		return nil, err
	case <-timer.C:
		return nil, errors.New("rtpffmpeg decoder: timed out waiting for decoded frame")
	case <-done:
		return nil, errors.New("rtpffmpeg decoder: decoder stopped")
	}
}

func (d *Decoder) stopLocked() {
	done := d.done
	stdin := d.stdin
	stdout := d.stdout
	cmd := d.cmd

	if done != nil {
		select {
		case <-done:
		default:
			close(done)
		}
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
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
