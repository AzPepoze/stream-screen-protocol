package server

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"streamscreen/internal/config"
	"streamscreen/internal/logger"
	"streamscreen/internal/video/codec/blocky"
	videoh264 "streamscreen/internal/video/codec/h264"
	"streamscreen/internal/video/stream"
)

// Sender handles video encoding and custom protocol transmission.
type Sender struct {
	cfg  config.ServerConfig
	conn *net.UDPConn

	viewersMu sync.RWMutex
	viewers   map[string]*viewerState

	captureStop    func()
	frameSeq       uint32
	buffer         *PacketBuffer
	tileBuffer     *TileBuffer
	ctx            context.Context
	cancel         context.CancelFunc
	minFramePeriod time.Duration
	frameDeadline  time.Duration
	clientTimeout  time.Duration
	codecName      string
	blockyPipeline *blocky.ServerPipeline
	h264Pipeline   *videoh264.ServerPipeline
	audioCancel    context.CancelFunc
}

func NewSender(cfg config.ServerConfig) (*Sender, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(cfg.BindHost), Port: cfg.Port})
	if err != nil {
		return nil, err
	}
	_ = conn.SetWriteBuffer(4 * 1024 * 1024)

	ctx, cancel := context.WithCancel(context.Background())

	var gridSize int
	if v, ok := cfg.Capture.RGBACodecConfig["tile_size"]; ok {
		if val, ok := v.(int); ok {
			gridSize = val
		} else if val, ok := v.(float64); ok {
			gridSize = int(val)
		}
	}
	if gridSize == 0 {
		gridSize = 10
	}
	tileBuffer := NewTileBuffer(gridSize, cfg.Capture.Width, cfg.Capture.Height)
	blockyPipeline := blocky.NewServerPipeline(tileBuffer, cfg.Capture.RGBACodecConfig)

	codecName := cfg.Capture.Codec
	if codecName == "" {
		codecName = "blocky"
	}

	framePeriod := time.Second / time.Duration(cfg.Capture.FPS)
	s := &Sender{
		cfg:            cfg,
		conn:           conn,
		viewers:        make(map[string]*viewerState),
		buffer:         NewPacketBuffer(50000),
		tileBuffer:     tileBuffer,
		ctx:            ctx,
		cancel:         cancel,
		minFramePeriod: framePeriod,
		frameDeadline:  frameDeadlineForFPS(cfg.Capture.FPS),
		clientTimeout:  5 * time.Second,
		codecName:      codecName,
		blockyPipeline: blockyPipeline,
	}
	return s, nil
}

func frameDeadlineForFPS(fps int) time.Duration {
	if fps <= 0 {
		fps = 60
	}
	deadline := 4 * (time.Second / time.Duration(fps))
	if deadline < 25*time.Millisecond {
		deadline = 25 * time.Millisecond
	}
	if deadline > 100*time.Millisecond {
		deadline = 100 * time.Millisecond
	}
	return deadline
}

func (s *Sender) nextFrameSeq() uint32 {
	return atomic.AddUint32(&s.frameSeq, 1)
}

func (s *Sender) currentFrameSeq() uint32 {
	return atomic.LoadUint32(&s.frameSeq)
}

func (s *Sender) StartControlPlane() {
	go s.listenForNACKs()
	go s.viewerCleanupLoop()
}

// ProcessRGBAFrame routes one captured frame through the selected codec. A
// frame is captured/encoded once and the resulting packet batch is fanned out
// to all active viewers by independent per-viewer send queues.
func (s *Sender) ProcessRGBAFrame(rgbaData []byte) {
	if s.viewerCount() == 0 {
		return
	}
	expectedSize := s.cfg.Capture.Width * s.cfg.Capture.Height * 4
	if len(rgbaData) != expectedSize {
		logger.Info("[server] WARN: buffer size mismatch - got %d bytes, expected %d (%dx%dx4)", len(rgbaData), expectedSize, s.cfg.Capture.Width, s.cfg.Capture.Height)
		return
	}

	if s.codecName == "h264" {
		if err := s.SendH264Frame(rgbaData, s.cfg.Capture.Width, s.cfg.Capture.Height); err != nil {
			logger.Info("[server] h264 send failed: %v", err)
		}
		return
	}

	changedTiles := s.tileBuffer.UpdateTiles(rgbaData)
	if changedTiles == nil {
		changedTiles = []uint16{}
	}
	tilesToSend := s.tileBuffer.GetTilesToSend(changedTiles)
	if len(tilesToSend) == 0 {
		return
	}

	frameSeq := s.nextFrameSeq()
	packets, err := s.blockyPipeline.BuildTilesBatch(frameSeq, tilesToSend, stream.NowTimestampMS())
	if err != nil {
		logger.Info("[server] blocky packetization failed: %v", err)
		return
	}
	s.broadcastVideoBatch(packets, frameSeq)
}

func (s *Sender) Stop() error {
	s.cancel()
	if s.audioCancel != nil {
		s.audioCancel()
	}
	s.stopAllViewers()
	_ = s.CloseH264Pipeline()
	if s.captureStop != nil {
		s.captureStop()
	}
	return s.conn.Close()
}

// activeDestination is retained for older internal call sites while the
// transport migrates to batch fan-out. New media paths should use viewers.
func (s *Sender) activeDestination() *net.UDPAddr {
	viewers := s.activeViewers()
	if len(viewers) == 0 {
		return nil
	}
	return viewers[0].addr
}

func (s *Sender) setDestinationAndSeen(addr *net.UDPAddr) {
	s.registerViewer(addr)
}
