package client

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"streamscreen/internal/audio/opus"
	"streamscreen/internal/audio/playback"
	"streamscreen/internal/config"
	"streamscreen/internal/logger"
	"streamscreen/internal/video/codec/blocky"
	videoh264 "streamscreen/internal/video/codec/h264"
)

// ClientReceiver handles jitter buffering, FEC recovery, NACKs, and frame decoding.
type ClientReceiver struct {
	cfg           config.ClientConfig
	conn          *net.UDPConn
	serverAddr    *net.UDPAddr
	jitterBuffer  *JitterBuffer
	fecRecoverer  *FECRecoverer
	tileGrid      *TileGrid
	tileFragBuf   map[string]*TileFragmentBuffer
	tileFragBufMu sync.RWMutex
	frameBuffer   []byte
	frameBufferMu sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	pixels        []byte
	prevPixels    []byte
	pixelsMu      sync.RWMutex
	frameSeq      uint64
	frameChan     chan assembledFrame
	tileGridSize  int
	videoWidth    uint32
	videoHeight   uint32
	videoFPS      uint32
	codecName     string
	videoInfoMu   sync.RWMutex
	blockyPipeline *blocky.ClientPipeline
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

	ccFrameDrops      uint64
	ccAudioDrops      uint64
	ccNACKSent        uint64
	ccFECRecovered    uint64
	ccPacketsReceived uint64
	ccBytesReceived   uint64
	ccRTTMS           uint32
	ccProbeNonce      uint32
}

type audioFragmentBuffer struct {
	fragments    map[uint32][]byte
	totalPackets uint32
	receivedAt   time.Time
}

// TileFragmentBuffer holds reassembly data for fragmented tiles.
type TileFragmentBuffer struct {
	fragments    map[uint32][]byte
	totalPackets uint32
	tileID       uint16
	receivedAt   time.Time
}

func NewClientReceiver(cfg config.ClientConfig) (*ClientReceiver, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: 0})
	if err != nil {
		return nil, err
	}
	_ = conn.SetReadBuffer(4 * 1024 * 1024)

	serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.ServerHost, cfg.Port))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	maxLatency := time.Duration(cfg.Network.MaxLatencyMS) * time.Millisecond
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        maxLatency,
		LossTolerance:     0.1,
		NackRetryDelay:    time.Duration(cfg.Network.NackRetryMS) * time.Millisecond,
		PartialFrameReady: cfg.Network.PartialFrameReady,
		AllowPartial:      cfg.Network.AllowPartial,
		ForceOutput:       cfg.Network.ForceOutput,
	})
	fecMaxAge := maxLatency * 2
	if fecMaxAge < 100*time.Millisecond {
		fecMaxAge = 100 * time.Millisecond
	}
	return &ClientReceiver{
		cfg:           cfg,
		conn:          conn,
		serverAddr:    serverAddr,
		jitterBuffer:  jb,
		fecRecoverer:  NewFECRecoverer(fecMaxAge),
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
	go r.receiveLoop()
	go r.nackLoop()
	go r.joinLoop()
	go r.controlLoop()

	logger.Info("Client: Start() waiting for server VideoInfo (timeout=30s)")
	deadline := time.Now().Add(30 * time.Second)
	for {
		r.videoInfoMu.RLock()
		if r.videoWidth > 0 && r.videoHeight > 0 && r.videoFPS > 0 {
			width, height, fps := r.videoWidth, r.videoHeight, r.videoFPS
			r.videoInfoMu.RUnlock()
			logger.Info("Client: Start() GOT server video info: %dx%d @ %d fps", width, height, fps)

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
			logger.Info("Client: initialized TileGrid %dx%d with %d tiles", r.tileGridSize, r.tileGridSize, r.tileGridSize*r.tileGridSize)
			logger.Info("Client: initialized frame buffer: %d bytes", pixelSize)

			if r.currentCodecName() == "h264" {
				logger.Info("Client: codec=h264, enabling H264 decode/render path")
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

			r.blockyPipeline = blocky.NewClientPipeline(nil)
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
	maxLatency, nackRetry := autoJitterTiming(
		fps,
		time.Duration(r.cfg.Network.MaxLatencyMS)*time.Millisecond,
		time.Duration(r.cfg.Network.NackRetryMS)*time.Millisecond,
	)
	r.jitterBuffer.ConfigureTiming(maxLatency, nackRetry)
	logger.Info("Client: auto network timing fps=%d max_latency=%s nack_retry=%s", fps, maxLatency, nackRetry)
}

func autoJitterTiming(fps int, configuredMaxLatency, configuredNACK time.Duration) (time.Duration, time.Duration) {
	if fps <= 0 {
		fps = 60
	}
	framePeriod := time.Second / time.Duration(fps)
	maxLatency := framePeriod * 4
	if maxLatency < 25*time.Millisecond {
		maxLatency = 25 * time.Millisecond
	}
	if maxLatency > 100*time.Millisecond {
		maxLatency = 100 * time.Millisecond
	}
	if configuredMaxLatency > 0 && configuredMaxLatency < maxLatency {
		maxLatency = configuredMaxLatency
	}

	nackRetry := framePeriod / 2
	if nackRetry < 3*time.Millisecond {
		nackRetry = 3 * time.Millisecond
	}
	if nackRetry > 15*time.Millisecond {
		nackRetry = 15 * time.Millisecond
	}
	if configuredNACK > 0 && configuredNACK < nackRetry {
		nackRetry = configuredNACK
	}
	return maxLatency, nackRetry
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
