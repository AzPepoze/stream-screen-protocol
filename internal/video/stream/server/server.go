package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"streamscreen/internal/config"
	videoh264 "streamscreen/internal/video/codec/h264"
	"streamscreen/internal/video/codec/rgba"
)

// Sender handles video encoding and custom protocol transmission.
type Sender struct {
	cfg               config.ServerConfig
	conn              *net.UDPConn
	destAddr          *net.UDPAddr
	destAddrMu        sync.RWMutex
	captureStop       func()
	frameSeq          uint32
	buffer            *PacketBuffer
	tileBuffer        *TileBuffer // For tile-based delta encoding
	ctx               context.Context
	cancel            context.CancelFunc
	minFramePeriod    time.Duration
	lastFrameAt       time.Time
	lastClientSeenAt  time.Time
	clientTimeout     time.Duration
	lastVideoInfoSent time.Time // Track when VideoInfo was last sent to avoid spamming
	lastAudioInfoSent time.Time // Track when AudioInfo was last sent to avoid spamming
	codecName         string    // Transmission codec (rgba or h264)
	rgbaPipeline      *rgba.ServerPipeline
	h264Pipeline      *videoh264.ServerPipeline
	audioCancel       context.CancelFunc
}

func NewSender(cfg config.ServerConfig, dest string) (*Sender, error) {
	var addr *net.UDPAddr
	if dest != "" {
		var err error
		addr, err = net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", dest, cfg.Port))
		if err != nil {
			return nil, err
		}
		if addr.IP.IsLoopback() && addr.Port == cfg.Port {
			log.Printf("Sender: configured destination %s appears to be local; ignoring to avoid self-send", addr.String())
			addr = nil
		}
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(cfg.BindHost), Port: cfg.Port})
	if err != nil {
		return nil, err
	}

	// Try to increase send buffer for high-throughput streaming.
	_ = conn.SetWriteBuffer(4 * 1024 * 1024)

	ctx, cancel := context.WithCancel(context.Background())

	// Initialize tile buffer for delta encoding
	gridSize := cfg.Video.TileGridSize
	if gridSize == 0 {
		gridSize = 10 // Default to 10x10 grid
	}
	tileBuffer := NewTileBuffer(gridSize, cfg.Capture.Width, cfg.Capture.Height)

	// Initialize RGBA pipeline
	rgbaPipeline := rgba.NewServerPipeline(tileBuffer, cfg.CodecConfig)

	// Get codec name from config (default: rgba)
	codecName := cfg.StreamCodec
	if codecName == "" {
		codecName = "rgba"
	}

	return &Sender{
		cfg:            cfg,
		conn:           conn,
		destAddr:       addr,
		buffer:         NewPacketBuffer(50000),
		tileBuffer:     tileBuffer,
		ctx:            ctx,
		cancel:         cancel,
		minFramePeriod: time.Second / time.Duration(cfg.Capture.FPS),
		clientTimeout:  5 * time.Second,
		codecName:      codecName,
		rgbaPipeline:   rgbaPipeline,
	}, nil
}

func (s *Sender) StartControlPlane() {
	go s.listenForNACKs()
}

// ProcessRGBAFrame routes raw RGBA frames through the configured transmission codec.
func (s *Sender) ProcessRGBAFrame(rgbaData []byte) {
	expectedSize := s.cfg.Capture.Width * s.cfg.Capture.Height * 4
	if len(rgbaData) != expectedSize {
		log.Printf("[server] WARN: buffer size mismatch - got %d bytes, expected %d (%dx%dx4)", len(rgbaData), expectedSize, s.cfg.Capture.Width, s.cfg.Capture.Height)
		return
	}

	// Handle based on codec type
	if s.codecName == "rgba" && s.rgbaPipeline != nil {
		// RGBA tile-based transmission
		changedTiles := s.tileBuffer.UpdateTiles(rgbaData)
		if changedTiles == nil {
			changedTiles = []uint16{}
		}
		tilesToSend := s.tileBuffer.GetTilesToSend(changedTiles)

		if len(tilesToSend) > 0 {
			destAddr := s.activeDestination()
			if destAddr != nil {
				s.frameSeq++
				s.rgbaPipeline.SendTilesBurst(s.frameSeq, tilesToSend, s.conn, destAddr)
			}
		}
	} else if s.codecName == "h264" {
		// H264 frame-based transmission: encode and send full frame packets.
		if err := s.SendH264Frame(rgbaData, s.cfg.Capture.Width, s.cfg.Capture.Height); err != nil {
			log.Printf("[server] h264 send failed: %v", err)
		}
	} else {
		// Fallback to tile-based (RGBA)
		changedTiles := s.tileBuffer.UpdateTiles(rgbaData)
		if changedTiles == nil {
			changedTiles = []uint16{}
		}
		tilesToSend := s.tileBuffer.GetTilesToSend(changedTiles)

		if len(tilesToSend) > 0 {
			destAddr := s.activeDestination()
			if destAddr != nil {
				s.frameSeq++
				s.rgbaPipeline.SendTilesBurst(s.frameSeq, tilesToSend, s.conn, destAddr)
			}
		}
	}
}

func (s *Sender) Stop() error {
	s.cancel()
	if s.audioCancel != nil {
		s.audioCancel()
	}
	_ = s.CloseH264Pipeline()
	if s.captureStop != nil {
		s.captureStop()
	}
	return s.conn.Close()
}

func (s *Sender) activeDestination() *net.UDPAddr {
	s.destAddrMu.Lock()
	defer s.destAddrMu.Unlock()

	if s.destAddr == nil {
		return nil
	}
	if !s.lastClientSeenAt.IsZero() && time.Since(s.lastClientSeenAt) > s.clientTimeout {
		log.Printf("[server] client timed out after %s, stopping stream until reconnect", s.clientTimeout)
		s.destAddr = nil
		return nil
	}
	return s.destAddr
}

func (s *Sender) setDestinationAndSeen(addr *net.UDPAddr) {
	s.destAddrMu.Lock()
	defer s.destAddrMu.Unlock()
	s.destAddr = addr
	s.lastClientSeenAt = time.Now()
}
