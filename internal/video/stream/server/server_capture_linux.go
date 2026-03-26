//go:build linux

package server

import (
	"fmt"
	"log"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

func (s *Sender) Start(pipewireFD int, nodeID uint32) error {
	gst.Init(nil)

	pipelineStr := fmt.Sprintf(
		"pipewiresrc fd=%d path=%d do-timestamp=true ! "+
			"queue leaky=downstream max-size-buffers=2 ! "+
			"videoconvert ! "+
			"videoscale ! video/x-raw,width=%d,height=%d,format=RGBA ! "+
			"appsink name=sink sync=false async=false emit-signals=true",
		pipewireFD, nodeID, s.cfg.Capture.Width, s.cfg.Capture.Height,
	)

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		return err
	}

	sinkElem, err := pipeline.GetElementByName("sink")
	if err != nil {
		_ = pipeline.SetState(gst.StateNull)
		return err
	}
	appsink := app.SinkFromElement(sinkElem)
	if appsink == nil {
		_ = pipeline.SetState(gst.StateNull)
		return fmt.Errorf("failed to get appsink")
	}

	appsink.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: s.onRawSample,
	})

	s.captureStop = func() {
		_ = pipeline.SetState(gst.StateNull)
	}

	s.StartControlPlane()
	log.Printf("[server] starting legacy linux capture pipeline")
	return pipeline.SetState(gst.StatePlaying)
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

	s.ProcessRGBAFrame(buffer.Bytes())
	return gst.FlowOK
}
