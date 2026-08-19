package rtpffmpeg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sync"
	"time"
)

// Config describes a persistent FFmpeg H.264 encoder session. FFmpeg sends
// encoded H.264 to a loopback RTP socket so RTP's marker bit gives us a
// reliable access-unit boundary without restarting FFmpeg for every frame.
type Config struct {
	Codec       string
	InputFormat string
	FPS         int
	Width       int
	Height      int
	// GlobalArgs are inserted before the rawvideo input. Hardware-device
	// options such as -vaapi_device need this position.
	GlobalArgs []string
	// ExtraArgs are inserted after encoder selection and before RTP output.
	ExtraArgs []string
}

type Session struct {
	cfg Config

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	rtpConn *net.UDPConn
	frames  chan []byte
	errCh   chan error
	done    chan struct{}
	closed  bool
}

func New(cfg Config) (*Session, error) {
	if cfg.Codec == "" {
		return nil, errors.New("rtpffmpeg: codec is required")
	}
	if cfg.InputFormat == "" {
		cfg.InputFormat = "rgba"
	}
	if cfg.FPS <= 0 {
		cfg.FPS = 60
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("rtpffmpeg: invalid dimensions %dx%d", cfg.Width, cfg.Height)
	}

	s := &Session{
		cfg:    cfg,
		frames: make(chan []byte, 4),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
	}
	if err := s.start(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) start() error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		return fmt.Errorf("rtpffmpeg: listen RTP: %w", err)
	}
	_ = conn.SetReadBuffer(2 * 1024 * 1024)

	port := conn.LocalAddr().(*net.UDPAddr).Port
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
	}
	args = append(args, s.cfg.GlobalArgs...)
	args = append(args,
		"-f", "rawvideo",
		"-pix_fmt", s.cfg.InputFormat,
		"-video_size", fmt.Sprintf("%dx%d", s.cfg.Width, s.cfg.Height),
		"-framerate", fmt.Sprintf("%d", s.cfg.FPS),
		"-i", "pipe:0",
		"-an",
		"-c:v", s.cfg.Codec,
	)
	args = append(args, s.cfg.ExtraArgs...)
	args = append(args,
		"-flush_packets", "1",
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-f", "rtp",
		"-payload_type", "96",
		fmt.Sprintf("rtp://127.0.0.1:%d?pkt_size=1200", port),
	)

	cmd := exec.Command("ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("rtpffmpeg: stdin pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = conn.Close()
		return fmt.Errorf("rtpffmpeg: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = conn.Close()
		return fmt.Errorf("rtpffmpeg: start FFmpeg: %w", err)
	}

	s.cmd = cmd
	s.stdin = stdin
	s.rtpConn = conn

	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	go s.readRTP()
	go func() {
		err := cmd.Wait()
		select {
		case <-s.done:
			return
		default:
		}
		if err == nil {
			err = errors.New("FFmpeg exited unexpectedly")
		}
		select {
		case s.errCh <- fmt.Errorf("rtpffmpeg: %w", err):
		default:
		}
	}()

	return nil
}

// Encode writes exactly one raw frame into the long-lived encoder and waits
// for the RTP marker that terminates the corresponding H.264 access unit.
// The timeout is only a failure guard. It is intentionally not the media
// latency budget: starting FFmpeg/NVENC may take hundreds of milliseconds on
// the first frame, while subsequent frames reuse the same initialized session.
func (s *Session) Encode(frame []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, errors.New("rtpffmpeg: encoder is closed")
	}
	expected := s.cfg.Width * s.cfg.Height * 4
	if len(frame) != expected {
		return nil, fmt.Errorf("rtpffmpeg: invalid raw frame size got=%d expected=%d", len(frame), expected)
	}

	if _, err := s.stdin.Write(frame); err != nil {
		return nil, fmt.Errorf("rtpffmpeg: write raw frame: %w", err)
	}

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	select {
	case accessUnit := <-s.frames:
		if len(accessUnit) == 0 {
			return nil, errors.New("rtpffmpeg: empty H.264 access unit")
		}
		return accessUnit, nil
	case err := <-s.errCh:
		return nil, err
	case <-timer.C:
		return nil, errors.New("rtpffmpeg: timed out waiting for encoded frame after 2s")
	case <-s.done:
		return nil, errors.New("rtpffmpeg: encoder closed")
	}
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.done)
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.rtpConn != nil {
		_ = s.rtpConn.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}

func (s *Session) readRTP() {
	buf := make([]byte, 64*1024)
	var accessUnit []byte
	var currentTS uint32
	var haveTS bool

	for {
		n, _, err := s.rtpConn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			select {
			case s.errCh <- fmt.Errorf("rtpffmpeg: RTP read: %w", err):
			default:
			}
			return
		}
		if n < 12 {
			continue
		}

		_, marker, timestamp, payload, ok := parseRTPPacket(buf[:n])
		if !ok || len(payload) == 0 {
			continue
		}
		if !haveTS || timestamp != currentTS {
			accessUnit = accessUnit[:0]
			currentTS = timestamp
			haveTS = true
		}

		var depacketErr error
		accessUnit, depacketErr = appendH264RTPPayload(accessUnit, payload)
		if depacketErr != nil {
			accessUnit = accessUnit[:0]
			haveTS = false
			continue
		}
		if marker {
			frame := append([]byte(nil), accessUnit...)
			accessUnit = accessUnit[:0]
			haveTS = false
			select {
			case s.frames <- frame:
			case <-s.done:
				return
			}
		}
	}
}

func parseRTPPacket(packet []byte) (headerLen int, marker bool, timestamp uint32, payload []byte, ok bool) {
	if len(packet) < 12 || packet[0]>>6 != 2 {
		return 0, false, 0, nil, false
	}
	cc := int(packet[0] & 0x0f)
	headerLen = 12 + cc*4
	if len(packet) < headerLen {
		return 0, false, 0, nil, false
	}
	if packet[0]&0x10 != 0 {
		if len(packet) < headerLen+4 {
			return 0, false, 0, nil, false
		}
		extWords := int(binary.BigEndian.Uint16(packet[headerLen+2 : headerLen+4]))
		headerLen += 4 + extWords*4
		if len(packet) < headerLen {
			return 0, false, 0, nil, false
		}
	}
	end := len(packet)
	if packet[0]&0x20 != 0 {
		pad := int(packet[len(packet)-1])
		if pad == 0 || pad > end-headerLen {
			return 0, false, 0, nil, false
		}
		end -= pad
	}
	marker = packet[1]&0x80 != 0
	timestamp = binary.BigEndian.Uint32(packet[4:8])
	return headerLen, marker, timestamp, packet[headerLen:end], true
}

var annexBStartCode = []byte{0, 0, 0, 1}

func appendH264RTPPayload(dst, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return dst, errors.New("empty RTP payload")
	}
	nalType := payload[0] & 0x1f
	switch {
	case nalType >= 1 && nalType <= 23:
		dst = append(dst, annexBStartCode...)
		dst = append(dst, payload...)
		return dst, nil
	case nalType == 24:
		pos := 1
		for pos+2 <= len(payload) {
			n := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
			pos += 2
			if n <= 0 || pos+n > len(payload) {
				return dst, errors.New("invalid STAP-A NAL length")
			}
			dst = append(dst, annexBStartCode...)
			dst = append(dst, payload[pos:pos+n]...)
			pos += n
		}
		if pos != len(payload) {
			return dst, errors.New("truncated STAP-A payload")
		}
		return dst, nil
	case nalType == 28:
		if len(payload) < 2 {
			return dst, errors.New("truncated FU-A payload")
		}
		indicator := payload[0]
		fuHeader := payload[1]
		if fuHeader&0x80 != 0 {
			reconstructedNAL := (indicator & 0xe0) | (fuHeader & 0x1f)
			dst = append(dst, annexBStartCode...)
			dst = append(dst, reconstructedNAL)
		}
		dst = append(dst, payload[2:]...)
		return dst, nil
	default:
		return dst, fmt.Errorf("unsupported H.264 RTP NAL type %d", nalType)
	}
}
