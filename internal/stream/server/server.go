package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"

	"streamscreen/internal/codec/h264"
	"streamscreen/internal/codec/rgba"
	"streamscreen/internal/config"
)

// Sender handles video encoding and custom protocol transmission.
type Sender struct {
	cfg               config.ServerConfig
	conn              *net.UDPConn
	destAddr          *net.UDPAddr
	destAddrMu        sync.RWMutex
	pipeline          *gst.Pipeline
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
	codecName         string    // Transmission codec (rgba or h264)
	rgbaPipeline      *rgba.ServerPipeline
	h264Pipeline      *h264.ServerPipeline
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

	// Optimize socket buffers for high-throughput streaming
	if rawConn, err := conn.SyscallConn(); err == nil {
		rawConn.Control(func(fd uintptr) {
			// Try to increase send buffer (ignore errors if not supported)
			syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 4*1024*1024) // 4MB
		})
	}

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

func (s *Sender) Start(pipewireFD int, nodeID uint32) error {
	gst.Init(nil)

	// Tile-based delta encoding pipeline: raw RGBA capture with per-tile change detection
	pipelineStr := fmt.Sprintf(
		"pipewiresrc fd=%d path=%d do-timestamp=true ! "+
			"queue leaky=downstream max-size-buffers=2 ! "+
			"videoconvert ! "+
			"videoscale ! video/x-raw,width=%d,height=%d,format=RGBA ! "+
			"appsink name=sink sync=false async=false emit-signals=true",
		pipewireFD, nodeID, s.cfg.Capture.Width, s.cfg.Capture.Height,
	)

	log.Printf("[server] Creating pipeline with geometry %dx%d", s.cfg.Capture.Width, s.cfg.Capture.Height)

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		return err
	}
	s.pipeline = pipeline

	sinkElem, err := pipeline.GetElementByName("sink")
	if err != nil {
		return err
	}
	appsink := app.SinkFromElement(sinkElem)
	if appsink == nil {
		log.Printf("[server] ERROR: appsink is nil")
		return fmt.Errorf("failed to get appsink")
	}

	appsink.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: s.onRawSample,
	})
	log.Printf("[server] Callback registered for NewSample")

	go s.listenForNACKs()

	log.Printf("[server] Starting GStreamer pipeline...")
	return s.pipeline.SetState(gst.StatePlaying)
}

func (s *Sender) onRawSample(sink *app.Sink) gst.FlowReturn {
	sample := sink.PullSample()
	if sample == nil {
		return gst.FlowError
	}

	buffer := sample.GetBuffer()
	if buffer == nil {
		return gst.FlowOK
	}

	// Extract raw RGBA pixel data
	rgbaData := buffer.Bytes()
	expectedSize := s.cfg.Capture.Width * s.cfg.Capture.Height * 4

	if len(rgbaData) != expectedSize {
		log.Printf("[server] WARN: buffer size mismatch - got %d bytes, expected %d (%dx%dx4)", len(rgbaData), expectedSize, s.cfg.Capture.Width, s.cfg.Capture.Height)
		return gst.FlowOK
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
			} else {
				log.Printf("[server] no destination address, skipping tile transmission")
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
			} else {
				log.Printf("[server] no destination address, skipping tile transmission")
			}
		}
	}

	return gst.FlowOK
}

func (s *Sender) Stop() error {
	s.cancel()
	if s.pipeline != nil {
		_ = s.pipeline.SetState(gst.StateNull)
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
