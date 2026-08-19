package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"streamscreen/internal/config"
	videoh264 "streamscreen/internal/video/codec/h264"
)

type mockStreamingDecoder struct {
	mu           sync.Mutex
	handler      videoh264.FrameHandler
	pushedFrames [][]byte
	failNextPush bool
}

func (m *mockStreamingDecoder) Decode(encodedData []byte, width, height int) ([]byte, error) {
	return nil, nil
}

func (m *mockStreamingDecoder) Push(encodedData []byte, width, height int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNextPush {
		return errors.New("simulated fatal decoder error")
	}
	m.pushedFrames = append(m.pushedFrames, encodedData)
	return nil
}

func (m *mockStreamingDecoder) SetOutputHandler(handler videoh264.FrameHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handler = handler
}

func (m *mockStreamingDecoder) EmitFrame(rgba []byte, width, height int) {
	m.mu.Lock()
	h := m.handler
	m.mu.Unlock()
	if h != nil {
		h(rgba, width, height)
	}
}

func (m *mockStreamingDecoder) Close() error {
	return nil
}

func setupTestReceiver(t *testing.T) (*ClientReceiver, *mockStreamingDecoder) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	rcv := &ClientReceiver{
		cfg:               config.ClientConfig{},
		conn:              conn,
		serverAddr:        serverConn.LocalAddr().(*net.UDPAddr),
		jitterBuffer:      NewJitterBuffer(50*time.Millisecond, 0.1),
		ctx:               ctx,
		cancel:            cancel,
		pixels:            make([]byte, 64*64*4),
		prevPixels:        make([]byte, 64*64*4),
		frameChan:         make(chan assembledFrame, 16),
		h264DecodedFrames: make(chan []byte, 2),
		videoWidth:        64,
		videoHeight:       64,
		videoFPS:          60,
		codecName:         "h264",
	}

	mockDec := &mockStreamingDecoder{}
	pipeline, err := videoh264.NewClientPipelineWithDecoder(mockDec, videoh264.Config{})
	if err != nil {
		t.Fatal(err)
	}

	pipeline.SetOutputHandler(func(rgbaData []byte, width, height int) {
		select {
		case rcv.h264DecodedFrames <- rgbaData:
		default:
			select {
			case <-rcv.h264DecodedFrames:
			default:
			}
			select {
			case rcv.h264DecodedFrames <- rgbaData:
			default:
			}
		}
	})
	rcv.h264Pipeline = pipeline

	go rcv.h264OutputLoop()
	go rcv.appsrcLoop()

	t.Cleanup(func() {
		cancel()
		_ = conn.Close()
		_ = serverConn.Close()
	})

	return rcv, mockDec
}

func TestAsyncDecoderNonBlockingPushAndEmission(t *testing.T) {
	rcv, mockDec := setupTestReceiver(t)

	au1 := []byte("au-frame-1")
	start := time.Now()
	// Push AU 1
	rcv.enqueueFrameLatest(assembledFrame{Seq: 1, Data: au1})
	pushDur := time.Since(start)
	if pushDur > 50*time.Millisecond {
		t.Fatalf("push blocked too long: %s", pushDur)
	}

	// No output emitted yet -> frameSeq must be 0 and no keyframe requested
	time.Sleep(15 * time.Millisecond)
	_, seq := rcv.Pixels()
	if seq != 0 {
		t.Fatalf("frameSeq should be 0 before decoder emits output, got %d", seq)
	}

	// Push AU 2
	au2 := []byte("au-frame-2")
	rcv.enqueueFrameLatest(assembledFrame{Seq: 2, Data: au2})

	// Simulate GStreamer emitting decoded RGBA frame asynchronously
	sample := make([]byte, 64*64*4)
	for i := range sample {
		sample[i] = 0x88
	}
	mockDec.EmitFrame(sample, 64, 64)

	// Verify client frameSeq advances asynchronously
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, seq = rcv.Pixels()
		if seq == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if seq != 1 {
		t.Fatalf("frameSeq failed to advance after async emission, got %d", seq)
	}
}

func TestDecoderErrorTriggersKeyframeWithRateLimiting(t *testing.T) {
	rcv, mockDec := setupTestReceiver(t)

	// Configure mock decoder to fail push
	mockDec.mu.Lock()
	mockDec.failNextPush = true
	mockDec.mu.Unlock()

	// Push frame that will fail in decoder
	rcv.enqueueFrameLatest(assembledFrame{Seq: 1, Data: []byte("corrupt-au")})
	time.Sleep(20 * time.Millisecond)

	rcv.pliMu.Lock()
	firstPli := rcv.lastPliAt
	rcv.pliMu.Unlock()

	if firstPli.IsZero() {
		t.Fatal("expected PLI request on decoder failure")
	}

	// Immediate second failure should be throttled by 1000ms rate limiter
	rcv.enqueueFrameLatest(assembledFrame{Seq: 2, Data: []byte("corrupt-au-2")})
	time.Sleep(20 * time.Millisecond)

	rcv.pliMu.Lock()
	secondPli := rcv.lastPliAt
	rcv.pliMu.Unlock()

	if secondPli != firstPli {
		t.Fatalf("PLI was not rate-limited: first=%v second=%v", firstPli, secondPli)
	}
}
