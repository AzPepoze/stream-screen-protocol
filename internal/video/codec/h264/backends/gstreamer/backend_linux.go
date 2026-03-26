//go:build linux

package gstreamer

import (
	"fmt"
	"time"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

type Encoder struct {
	pipeline  *gst.Pipeline
	appsrc    *app.Source
	appsink   *app.Sink
	width     int
	height    int
	nextPTS   gst.ClockTime
	frameDur  gst.ClockTime
	bitrate   int
	preset    string
	tune      string
	keyIntMax int
}

type Decoder struct {
	pipeline *gst.Pipeline
	appsrc   *app.Source
	appsink  *app.Sink
	nextPTS  gst.ClockTime
	frameDur gst.ClockTime
}

func NewEncoder(cfg map[string]interface{}) (*Encoder, error) {
	bitrate := intFrom(cfg, "bitrate", 5000)
	if bitrate <= 0 {
		bitrate = intFrom(cfg, "bitrate_kbps", 5000)
	}
	preset := stringFrom(cfg, "preset", "ultrafast")
	if preset == "" {
		preset = stringFrom(cfg, "speed_preset", "ultrafast")
	}
	if preset == "" {
		preset = "ultrafast"
	}
	tune := stringFrom(cfg, "tune", "zerolatency")
	if tune == "" {
		tune = "zerolatency"
	}
	fps := intFrom(cfg, "fps", 60)
	if fps <= 0 {
		fps = 60
	}
	keyIntMax := intFrom(cfg, "key-int-max", fps)
	if keyIntMax <= 0 {
		keyIntMax = intFrom(cfg, "key_int_max", fps)
	}
	if keyIntMax <= 0 {
		keyIntMax = fps
	}

	gst.Init(nil)

	pipelineStr := fmt.Sprintf(
		"appsrc name=src is-live=true format=time do-timestamp=true block=false ! "+
			"queue leaky=downstream max-size-buffers=2 ! videoconvert ! "+
			"x264enc bitrate=%d speed-preset=%s tune=%s key-int-max=%d bframes=0 byte-stream=true aud=true sliced-threads=true ! "+
			"video/x-h264,stream-format=byte-stream,alignment=au ! "+
			"appsink name=sink sync=false async=false max-buffers=1 drop=true",
		bitrate, preset, tune, keyIntMax,
	)

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		return nil, fmt.Errorf("h264: failed to create pipeline: %w", err)
	}

	appsrcElem, err := pipeline.GetElementByName("src")
	if err != nil {
		return nil, fmt.Errorf("h264: failed to get appsrc: %w", err)
	}
	appsinkElem, err := pipeline.GetElementByName("sink")
	if err != nil {
		return nil, fmt.Errorf("h264: failed to get appsink: %w", err)
	}
	appsrc := app.SrcFromElement(appsrcElem)
	appsink := app.SinkFromElement(appsinkElem)
	if appsrc == nil || appsink == nil {
		return nil, fmt.Errorf("h264: failed to convert appsrc/appsink elements")
	}

	if err := pipeline.SetState(gst.StatePlaying); err != nil {
		return nil, fmt.Errorf("h264: failed to set encoder pipeline playing: %w", err)
	}

	return &Encoder{
		pipeline:  pipeline,
		appsrc:    appsrc,
		appsink:   appsink,
		frameDur:  gst.ClockTime((time.Second / time.Duration(fps)).Nanoseconds()),
		bitrate:   bitrate,
		preset:    preset,
		tune:      tune,
		keyIntMax: keyIntMax,
	}, nil
}

func (e *Encoder) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	if e.pipeline == nil {
		return nil, fmt.Errorf("h264: encoder not initialized")
	}
	if len(rgbaData) != width*height*4 {
		return nil, fmt.Errorf("h264: invalid frame size: got %d, expected %d", len(rgbaData), width*height*4)
	}

	if e.width != width || e.height != height {
		fr := int((time.Second + time.Duration(e.frameDur)/2) / time.Duration(e.frameDur))
		e.appsrc.SetCaps(gst.NewCapsFromString(
			fmt.Sprintf("video/x-raw,format=RGBA,width=%d,height=%d,framerate=%d/1", width, height, fr),
		))
		e.width = width
		e.height = height
	}

	buffer := gst.NewBufferFromBytes(rgbaData)
	if buffer == nil {
		return nil, fmt.Errorf("h264: failed to create GStreamer buffer")
	}
	buffer.SetPresentationTimestamp(e.nextPTS)
	buffer.SetDuration(e.frameDur)
	e.nextPTS += e.frameDur

	ret := e.appsrc.PushBuffer(buffer)
	if ret != gst.FlowOK && ret != gst.FlowFlushing {
		return nil, fmt.Errorf("h264: appsrc push failed: %v", ret)
	}

	sample := e.appsink.TryPullSample(gst.ClockTime((500 * time.Millisecond).Nanoseconds()))
	if sample == nil {
		return nil, fmt.Errorf("h264: no encoded sample available")
	}
	buf := sample.GetBuffer()
	if buf == nil {
		return nil, fmt.Errorf("h264: failed to get buffer from sample")
	}
	return buf.Bytes(), nil
}

func (e *Encoder) Close() error {
	if e.pipeline != nil {
		e.pipeline.SetState(gst.StateNull)
	}
	return nil
}

func NewDecoder(cfg map[string]interface{}) (*Decoder, error) {
	fps := intFrom(cfg, "fps", 60)
	if fps <= 0 {
		fps = 60
	}
	gst.Init(nil)

	pipelineStr := "appsrc name=src is-live=true format=time do-timestamp=true block=false ! " +
		"queue leaky=downstream max-size-buffers=4 ! h264parse ! avdec_h264 ! videoconvert ! " +
		"video/x-raw,format=RGBA ! appsink name=sink sync=false async=false max-buffers=1 drop=true"

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		return nil, fmt.Errorf("h264: failed to create decoder pipeline: %w", err)
	}

	appsrcElem, err := pipeline.GetElementByName("src")
	if err != nil {
		return nil, fmt.Errorf("h264: failed to get appsrc: %w", err)
	}
	appsinkElem, err := pipeline.GetElementByName("sink")
	if err != nil {
		return nil, fmt.Errorf("h264: failed to get appsink: %w", err)
	}
	appsrc := app.SrcFromElement(appsrcElem)
	appsink := app.SinkFromElement(appsinkElem)
	if appsrc == nil || appsink == nil {
		return nil, fmt.Errorf("h264: failed to convert appsrc/appsink elements")
	}

	appsrc.SetCaps(gst.NewCapsFromString("video/x-h264,stream-format=byte-stream,alignment=au"))
	if err := pipeline.SetState(gst.StatePlaying); err != nil {
		return nil, fmt.Errorf("h264: failed to set decoder pipeline playing: %w", err)
	}

	return &Decoder{
		pipeline: pipeline,
		appsrc:   appsrc,
		appsink:  appsink,
		frameDur: gst.ClockTime((time.Second / time.Duration(fps)).Nanoseconds()),
	}, nil
}

func (d *Decoder) Decode(encodedData []byte, width, height int) ([]byte, error) {
	if d.pipeline == nil {
		return nil, fmt.Errorf("h264: decoder not initialized")
	}
	if len(encodedData) == 0 {
		return nil, fmt.Errorf("h264: empty encoded data")
	}

	buffer := gst.NewBufferFromBytes(encodedData)
	if buffer == nil {
		return nil, fmt.Errorf("h264: failed to create GStreamer buffer")
	}
	buffer.SetPresentationTimestamp(d.nextPTS)
	buffer.SetDuration(d.frameDur)
	d.nextPTS += d.frameDur

	ret := d.appsrc.PushBuffer(buffer)
	if ret != gst.FlowOK && ret != gst.FlowFlushing {
		return nil, fmt.Errorf("h264: appsrc push failed: %v", ret)
	}

	sample := d.appsink.TryPullSample(gst.ClockTime((500 * time.Millisecond).Nanoseconds()))
	if sample == nil {
		return nil, fmt.Errorf("h264: no decoded sample available")
	}
	buf := sample.GetBuffer()
	if buf == nil {
		return nil, fmt.Errorf("h264: failed to get buffer from sample")
	}

	decoded := buf.Bytes()
	expectedSize := width * height * 4
	if len(decoded) != expectedSize {
		return nil, fmt.Errorf("h264: decoded frame size mismatch: got %d, expected %d", len(decoded), expectedSize)
	}
	return decoded, nil
}

func (d *Decoder) Close() error {
	if d.pipeline != nil {
		d.pipeline.SetState(gst.StateNull)
	}
	return nil
}

func intFrom(cfg map[string]interface{}, key string, fallback int) int {
	if cfg == nil {
		return fallback
	}
	if v, ok := cfg[key]; ok {
		switch t := v.(type) {
		case int:
			return t
		case float64:
			return int(t)
		}
	}
	return fallback
}

func stringFrom(cfg map[string]interface{}, key, fallback string) string {
	if cfg == nil {
		return fallback
	}
	if v, ok := cfg[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}
