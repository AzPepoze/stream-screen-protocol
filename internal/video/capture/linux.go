//go:build linux

package capture

import (
	"context"
	"fmt"
	"log"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"

	"streamscreen/internal/config"
	"streamscreen/internal/video/portal"
)

type linuxSource struct {
	cfg      config.ServerConfig
	frames   chan []byte
	pipeline *gst.Pipeline
	session  *portal.ScreenCastSession
}

func newSource(cfg config.ServerConfig, backend config.CaptureBackend) (Source, error) {
	if backend != config.CaptureBackendPortalPipewire {
		return nil, fmt.Errorf("linux capture backend %q is not supported by internal capture", backend)
	}
	return &linuxSource{
		cfg:    cfg,
		frames: make(chan []byte, 8),
	}, nil
}

func (s *linuxSource) Start(ctx context.Context) error {
	gst.Init(nil)

	session, err := portal.StartScreenCast(ctx, s.cfg)
	if err != nil {
		return fmt.Errorf("portal screencast failed: %w", err)
	}
	if len(session.Streams) == 0 {
		_ = session.Close()
		return fmt.Errorf("portal returned no streams")
	}
	s.session = session

	streamInfo := session.Streams[0]
	log.Printf("capture(linux): portal stream_node=%d", streamInfo.NodeID)

	pipelineStr := fmt.Sprintf(
		"pipewiresrc fd=%d path=%d do-timestamp=true ! "+
			"queue leaky=downstream max-size-buffers=2 ! "+
			"videoconvert ! "+
			"videoscale ! video/x-raw,width=%d,height=%d,format=RGBA ! "+
			"appsink name=sink sync=false async=false emit-signals=true",
		int(session.RemoteFile().Fd()), streamInfo.NodeID, s.cfg.Capture.Width, s.cfg.Capture.Height,
	)

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		_ = session.Close()
		s.session = nil
		return fmt.Errorf("create pipeline: %w", err)
	}
	s.pipeline = pipeline

	sinkElem, err := pipeline.GetElementByName("sink")
	if err != nil {
		return err
	}
	appsink := app.SinkFromElement(sinkElem)
	if appsink == nil {
		return fmt.Errorf("failed to get appsink")
	}

	appsink.SetCallbacks(&app.SinkCallbacks{NewSampleFunc: s.onRawSample})

	if err := s.pipeline.SetState(gst.StatePlaying); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	return nil
}

func (s *linuxSource) Frames() <-chan []byte {
	return s.frames
}

func (s *linuxSource) Close() error {
	if s.pipeline != nil {
		_ = s.pipeline.SetState(gst.StateNull)
		s.pipeline = nil
	}
	if s.session != nil {
		err := s.session.Close()
		s.session = nil
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *linuxSource) onRawSample(sink *app.Sink) gst.FlowReturn {
	sample := sink.PullSample()
	if sample == nil {
		return gst.FlowError
	}

	buffer := sample.GetBuffer()
	if buffer == nil {
		return gst.FlowOK
	}

	frame := append([]byte(nil), buffer.Bytes()...)
	if len(frame) != s.cfg.Capture.Width*s.cfg.Capture.Height*4 {
		return gst.FlowOK
	}

	select {
	case s.frames <- frame:
	default:
		select {
		case <-s.frames:
		default:
		}
		select {
		case s.frames <- frame:
		default:
		}
	}

	return gst.FlowOK
}
