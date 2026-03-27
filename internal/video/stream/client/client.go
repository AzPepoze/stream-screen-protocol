package client

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"streamscreen/internal/audio/opus"
	"streamscreen/internal/audio/playback"
	"streamscreen/internal/config"
	videoh264 "streamscreen/internal/video/codec/h264"
	"streamscreen/internal/video/codec/rgba"
)

// ClientReceiver handles jitter buffering, NACKs, and frame decoding.
type ClientReceiver struct {
	cfg           config.ClientConfig
	conn          *net.UDPConn
	serverAddr    *net.UDPAddr
	jitterBuffer  *JitterBuffer
	tileGrid      *TileGrid                      // Tile-based screen buffer
	tileFragBuf   map[string]*TileFragmentBuffer // For reassembling fragmented tiles
	tileFragBufMu sync.RWMutex
	frameBuffer   []byte       // Full RGBA frame buffer (updated as tiles arrive)
	frameBufferMu sync.RWMutex // Protects frame buffer during tile writes
	ctx           context.Context
	cancel        context.CancelFunc
	pixels        []byte // Current display pixels
	prevPixels    []byte // Previous complete frame (fallback)
	pixelsMu      sync.RWMutex
	frameSeq      uint64
	frameChan     chan assembledFrame
	tileGridSize  int          // Tiles per side (3 = 3x3 grid)
	videoWidth    uint32       // Received from server
	videoHeight   uint32       // Received from server
	videoFPS      uint32       // Received from server
	codecName     string       // Codec type from server VideoInfo packet
	videoInfoMu   sync.RWMutex // Protects video info
	rgbaPipeline  *rgba.ClientPipeline
	h264Pipeline  *videoh264.ClientPipeline
	h264ErrMu     sync.Mutex
	h264ErrCount  uint64
	h264ErrLogAt  time.Time
	frameDirty    atomic.Bool
	autoTuneByFPS bool
	audioInfoMu   sync.RWMutex
	audioCodec    string
	audioRate     uint32
	audioChannels uint32
	audioFrameMS  uint32
	audioBitrate  uint32
	audioEnabled  bool
	audioDecoder  *opus.Decoder
	audioPlayer   playback.Player
	audioFrames   chan []byte
	audioFragMu   sync.Mutex
	audioFragBuf  map[uint32]*audioFragmentBuffer
}

type audioFragmentBuffer struct {
	fragments    map[uint32][]byte
	totalPackets uint32
	receivedAt   time.Time
}

// TileFragmentBuffer holds reassembly data for fragmented tiles
type TileFragmentBuffer struct {
	fragments    map[uint32][]byte // PacketID -> fragment data
	totalPackets uint32
	tileID       uint16 // Extracted from first fragment
	receivedAt   time.Time
}

func NewClientReceiver(cfg config.ClientConfig) (*ClientReceiver, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: 0})
	if err != nil {
		return nil, err
	}

	// Optimize socket buffers for high-throughput streaming
	_ = conn.SetReadBuffer(4 * 1024 * 1024)

	serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.ServerHost, cfg.Port))
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        time.Duration(cfg.Network.MaxLatencyMS) * time.Millisecond,
		LossTolerance:     0.1,
		NackRetryDelay:    time.Duration(cfg.Network.NackRetryMS) * time.Millisecond,
		PartialFrameReady: cfg.Network.PartialFrameReady,
		AllowPartial:      cfg.Network.AllowPartial,
		ForceOutput:       cfg.Network.ForceOutput,
	})
	return &ClientReceiver{
		cfg:           cfg,
		conn:          conn,
		serverAddr:    serverAddr,
		jitterBuffer:  jb,
		tileFragBuf:   make(map[string]*TileFragmentBuffer),
		ctx:           ctx,
		cancel:        cancel,
		pixels:        make([]byte, 0),
		prevPixels:    make([]byte, 0),
		frameChan:     make(chan assembledFrame, 128),
		tileGridSize:  3,
		autoTuneByFPS: cfg.Network.AutoTuneByFPS,
		audioEnabled:  cfg.Audio.Enabled,
		audioFrames:   make(chan []byte, 64),
		audioFragBuf:  make(map[uint32]*audioFragmentBuffer),
	}, nil
}

func (r *ClientReceiver) Start() error {
	// Start receive loop first to listen for server video info
	go r.receiveLoop()
	go r.nackLoop()
	go r.joinLoop()

	log.Printf("Client: Start() waiting for server VideoInfo (timeout=30s)")
	deadline := time.Now().Add(30 * time.Second)
	for {
		r.videoInfoMu.RLock()
		if r.videoWidth > 0 && r.videoHeight > 0 && r.videoFPS > 0 {
			width, height, fps := r.videoWidth, r.videoHeight, r.videoFPS
			r.videoInfoMu.RUnlock()
			log.Printf("Client: Start() GOT server video info: %dx%d @ %d fps", width, height, fps)

			r.applyJitterTimingFromFPS(int(fps))

			pixelSize := int(width * height * 4)
			r.pixelsMu.Lock()
			r.pixels = make([]byte, pixelSize)
			r.prevPixels = make([]byte, pixelSize)
			r.pixelsMu.Unlock()

			r.frameBufferMu.Lock()
			r.frameBuffer = make([]byte, pixelSize)
			r.frameBufferMu.Unlock()

			r.tileGrid = NewTileGrid(r.tileGridSize, int(width), int(height))
			log.Printf("Client: initialized TileGrid %dx%d with %d tiles", r.tileGridSize, r.tileGridSize, r.tileGridSize*r.tileGridSize)
			log.Printf("Client: initialized frame buffer: %d bytes", pixelSize)

			if r.currentCodecName() == "h264" {
				log.Printf("Client: codec=h264, enabling H264 decode/render path")
				if err := r.ensureH264Pipeline(); err != nil {
					return err
				}
				r.jitterBuffer.SetCompleteFramesOnly()
				go r.appsrcLoop()
				if err := r.startAudioPipeline(); err != nil {
					return err
				}
				return nil
			}

			r.rgbaPipeline = rgba.NewClientPipeline(nil)
			go r.appsrcLoop()
			go r.tileRequestLoop()
			go r.tileFrameReconstructionLoop()
			if err := r.startAudioPipeline(); err != nil {
				return err
			}
			return nil
		}
		r.videoInfoMu.RUnlock()

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for video info from server after 30s")
		}

		select {
		case <-time.After(100 * time.Millisecond):
			continue
		case <-r.ctx.Done():
			return fmt.Errorf("cancelled before video info received")
		}
	}
}

func (r *ClientReceiver) applyJitterTimingFromFPS(fps int) {
	if !r.autoTuneByFPS || fps <= 0 {
		return
	}

	framePeriod := time.Second / time.Duration(fps)
	maxLatency := framePeriod * 4
	if maxLatency < 200*time.Millisecond {
		maxLatency = 200 * time.Millisecond
	}
	nackRetry := framePeriod / 4
	if nackRetry < 20*time.Millisecond {
		nackRetry = 20 * time.Millisecond
	}
	if nackRetry > 250*time.Millisecond {
		nackRetry = 250 * time.Millisecond
	}
	r.jitterBuffer.ConfigureTiming(maxLatency, nackRetry)
}

func (r *ClientReceiver) Pixels() ([]byte, uint64) {
	r.pixelsMu.RLock()
	defer r.pixelsMu.RUnlock()
	return r.pixels, r.frameSeq
}

func (r *ClientReceiver) GetVideoResolution() (uint32, uint32) {
	r.videoInfoMu.RLock()
	defer r.videoInfoMu.RUnlock()
	return r.videoWidth, r.videoHeight
}

func (r *ClientReceiver) GetVideoFPS() uint32 {
	r.videoInfoMu.RLock()
	defer r.videoInfoMu.RUnlock()
	return r.videoFPS
}

func (r *ClientReceiver) Stop() error {
	r.cancel()
	if r.audioDecoder != nil {
		_ = r.audioDecoder.Close()
	}
	if r.audioPlayer != nil {
		_ = r.audioPlayer.Close()
	}
	_ = r.CloseH264Pipeline()
	return r.conn.Close()
}

func (r *ClientReceiver) currentCodecName() string {
	r.videoInfoMu.RLock()
	defer r.videoInfoMu.RUnlock()
	return r.codecName
}

func (r *ClientReceiver) startAudioPipeline() error {
	if !r.audioEnabled {
		return nil
	}
	decoder, err := opus.NewDecoder(r.cfg)
	if err != nil {
		return err
	}
	player, err := playback.New(r.cfg)
	if err != nil {
		_ = decoder.Close()
		return err
	}
	r.audioDecoder = decoder
	r.audioPlayer = player

	go r.audioLoop()
	return nil
}
