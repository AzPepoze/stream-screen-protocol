//go:build windows

package capture

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"streamscreen/internal/config"
)

type dxgiSource struct {
	cfg       config.ServerConfig
	handle    uintptr
	dll       *syscall.LazyDLL
	frames    chan []byte
	running   bool
	mu        sync.Mutex
	closeOnce sync.Once
}

type FrameMetadata struct {
	Width     uint32
	Height    uint32
	Timestamp uint64
	FrameSize uint32
}

func newDXGISource(cfg config.ServerConfig) (Source, error) {
	dll := syscall.NewLazyDLL("dxgi_capture.dll")
	initCapture := dll.NewProc("DXGI_InitCapture")
	getLastError := dll.NewProc("DXGI_GetLastError")

	outputIndex := 0
	if cfg.Capture.Source != "" && cfg.Capture.Source != "default" {
		if idx, err := parseOutputIndex(cfg.Capture.Source); err == nil {
			outputIndex = idx
		}
	}

	sharedMemName := fmt.Sprintf("Global\\DXGICapture_%d", time.Now().UnixNano())
	namePtr, err := syscall.BytePtrFromString(sharedMemName)
	if err != nil {
		return nil, fmt.Errorf("failed to create shared memory name: %w", err)
	}

	ret, _, _ := initCapture.Call(uintptr(outputIndex), uintptr(unsafe.Pointer(namePtr)))
	if ret == 0 {
		errMsg := getLastErrorString(getLastError)
		return nil, fmt.Errorf("failed to initialize DXGI capture: %s", errMsg)
	}

	return &dxgiSource{
		cfg:    cfg,
		handle: ret,
		dll:    dll,
		frames: make(chan []byte, 8),
	}, nil
}

func (s *dxgiSource) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("capture already running")
	}

	startCapture := s.dll.NewProc("DXGI_StartCapture")
	ret, _, _ := startCapture.Call(s.handle)
	if ret == 0 {
		return fmt.Errorf("failed to start capture")
	}
	s.running = true
	go s.frameReader(ctx)
	return nil
}

func (s *dxgiSource) Frames() <-chan []byte { return s.frames }

func (s *dxgiSource) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.running = false

		stopCapture := s.dll.NewProc("DXGI_StopCapture")
		_, _, _ = stopCapture.Call(s.handle)
		release := s.dll.NewProc("DXGI_Release")
		_, _, _ = release.Call(s.handle)
		close(s.frames)
	})
	return nil
}

func (s *dxgiSource) frameReader(ctx context.Context) {
	hasNewFrame := s.dll.NewProc("DXGI_HasNewFrame")
	getFrameData := s.dll.NewProc("DXGI_GetFrameData")
	releaseFrame := s.dll.NewProc("DXGI_ReleaseFrame")

	fps := s.cfg.Capture.FPS
	if fps <= 0 {
		fps = 60
	}
	pollInterval := time.Second / time.Duration(fps)
	// Keep a small floor so an accidental extreme FPS value cannot turn this
	// goroutine into a busy spin. 1ms still permits capture polling up to 1000Hz.
	if pollInterval < time.Millisecond {
		pollInterval = time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.running {
				return
			}

			ret, _, _ := hasNewFrame.Call(s.handle)
			if ret == 0 {
				continue
			}

			var metadata FrameMetadata
			if r1, _, _ := syscall.SyscallN(getFrameData.Addr(), s.handle, uintptr(unsafe.Pointer(&metadata))); r1 != 0 {
				frameSize := int(metadata.FrameSize)
				frameData := make([]byte, frameSize)
				srcPtr := (*[1 << 30]byte)(unsafe.Pointer(r1))[:frameSize:frameSize]
				copy(frameData, srcPtr)
				_, _, _ = releaseFrame.Call(s.handle)

				select {
				case s.frames <- frameData:
				default:
					// Drop newest capture if downstream is saturated. The media
					// pipeline remains bounded rather than accumulating latency.
				}
			}
		}
	}
}

func parseOutputIndex(source string) (int, error) {
	if idx := strings.Index(source, "output:"); idx >= 0 {
		return strconv.Atoi(source[idx+7:])
	}
	return strconv.Atoi(source)
}

func getLastErrorString(getLastError *syscall.LazyProc) string {
	if r1, _, _ := syscall.SyscallN(getLastError.Addr()); r1 != 0 {
		var bytes []byte
		ptr := (*byte)(unsafe.Pointer(r1))
		for {
			if b := *ptr; b == 0 {
				break
			} else {
				bytes = append(bytes, b)
				ptr = (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + 1))
			}
		}
		return string(bytes)
	}
	return "unknown error"
}
