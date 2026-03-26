package h264

import (
	"fmt"
	"time"

	"streamscreen/internal/codec"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

// H264Encoder encodes RGBA frames to H264 using GStreamer
type H264Encoder struct {
	bitrate   int
	preset    string
	tune      string
	keyIntMax int
	pipeline  *gst.Pipeline
	appsrc    *app.Source
	appsink   *app.Sink
	width     int
	height    int
	nextPTS   gst.ClockTime
	frameDur  gst.ClockTime
}

// NewH264Encoder creates a new H264 encoder
func NewH264Encoder(cfg codec.Config) (*H264Encoder, error) {
	bitrate := cfg.GetInt("bitrate", 5000)
	if bitrate <= 0 {
		bitrate = cfg.GetInt("bitrate_kbps", 5000)
	}
	preset := cfg.GetString("preset", "medium")
	if preset == "" {
		preset = cfg.GetString("speed_preset", "medium")
	}
	tune := cfg.GetString("tune", "zerolatency")
	keyIntMax := cfg.GetInt("key-int-max", 30)
	if keyIntMax <= 0 {
		keyIntMax = cfg.GetInt("key_int_max", 30)
	}
	if keyIntMax <= 0 {
		keyIntMax = 30
	}
	fps := cfg.GetInt("fps", 60)
	if fps <= 0 {
		fps = 60
	}

	// Initialize GStreamer
	gst.Init(nil)

	// Build GStreamer encoding pipeline:
	// appsrc (RGBA) -> videoconvert -> x264enc -> appsink (H264)
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

	// Get appsrc and appsink elements
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

	// Start pipeline so appsink can produce samples.
	if err := pipeline.SetState(gst.StatePlaying); err != nil {
		return nil, fmt.Errorf("h264: failed to set encoder pipeline playing: %w", err)
	}

	enc := &H264Encoder{
		bitrate:   bitrate,
		preset:    preset,
		tune:      tune,
		keyIntMax: keyIntMax,
		pipeline:  pipeline,
		appsrc:    appsrc,
		appsink:   appsink,
		frameDur:  gst.ClockTime((time.Second / time.Duration(fps)).Nanoseconds()),
	}

	return enc, nil
}

// Encode encodes RGBA frame to H264
func (e *H264Encoder) Encode(rgbaData []byte, width, height int) ([]byte, error) {
	if e.pipeline == nil {
		return nil, fmt.Errorf("h264: encoder not initialized")
	}

	if len(rgbaData) != width*height*4 {
		return nil, fmt.Errorf("h264: invalid frame size: got %d, expected %d", len(rgbaData), width*height*4)
	}

	// Keep caps aligned with input frame geometry.
	if e.width != width || e.height != height {
		e.appsrc.SetCaps(gst.NewCapsFromString(
			fmt.Sprintf("video/x-raw,format=RGBA,width=%d,height=%d,framerate=%d/1", width, height, int((time.Second+time.Duration(e.frameDur)/2)/time.Duration(e.frameDur))),
		))
		e.width = width
		e.height = height
	}

	// Create buffer from RGBA data
	buffer := gst.NewBufferFromBytes(rgbaData)
	if buffer == nil {
		return nil, fmt.Errorf("h264: failed to create GStreamer buffer")
	}
	buffer.SetPresentationTimestamp(e.nextPTS)
	buffer.SetDuration(e.frameDur)
	e.nextPTS += e.frameDur

	// Push to encoder
	ret := e.appsrc.PushBuffer(buffer)
	if ret != gst.FlowOK && ret != gst.FlowFlushing {
		return nil, fmt.Errorf("h264: appsrc push failed: %v", ret)
	}

	// Try to get encoded sample from sink
	sample := e.appsink.TryPullSample(gst.ClockTime((500 * time.Millisecond).Nanoseconds()))
	if sample == nil {
		return nil, fmt.Errorf("h264: no encoded sample available")
	}

	buf := sample.GetBuffer()
	if buf == nil {
		return nil, fmt.Errorf("h264: failed to get buffer from sample")
	}

	// Return encoded data
	return buf.Bytes(), nil
}

// CodecName returns "h264"
func (e *H264Encoder) CodecName() string {
	return "h264"
}

// Close stops the encoder and frees resources
func (e *H264Encoder) Close() error {
	if e.pipeline != nil {
		e.pipeline.SetState(gst.StateNull)
	}
	return nil
}

// H264Decoder decodes H264 data to RGBA using GStreamer
type H264Decoder struct {
	pipeline *gst.Pipeline
	appsrc   *app.Source
	appsink  *app.Sink
	nextPTS  gst.ClockTime
	frameDur gst.ClockTime
}

// NewH264Decoder creates a new H264 decoder
func NewH264Decoder(cfg codec.Config) (*H264Decoder, error) {
	fps := cfg.GetInt("fps", 60)
	if fps <= 0 {
		fps = 60
	}
	// Initialize GStreamer
	gst.Init(nil)

	// Build GStreamer decoding pipeline:
	// appsrc (H264) -> h264parse -> avdec_h264 -> videoconvert -> appsink (RGBA)
	pipelineStr := "appsrc name=src is-live=true format=time do-timestamp=true block=false ! " +
		"queue leaky=downstream max-size-buffers=4 ! h264parse ! avdec_h264 ! videoconvert ! " +
		"video/x-raw,format=RGBA ! appsink name=sink sync=false async=false max-buffers=1 drop=true"

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		return nil, fmt.Errorf("h264: failed to create decoder pipeline: %w", err)
	}

	// Get appsrc and appsink elements
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

	// Set caps for H264 input
	appsrc.SetCaps(gst.NewCapsFromString("video/x-h264,stream-format=byte-stream,alignment=au"))
	if err := pipeline.SetState(gst.StatePlaying); err != nil {
		return nil, fmt.Errorf("h264: failed to set decoder pipeline playing: %w", err)
	}

	dec := &H264Decoder{
		pipeline: pipeline,
		appsrc:   appsrc,
		appsink:  appsink,
		frameDur: gst.ClockTime((time.Second / time.Duration(fps)).Nanoseconds()),
	}

	return dec, nil
}

// Decode decodes H264 data to RGBA frame
func (d *H264Decoder) Decode(encodedData []byte, width, height int) ([]byte, error) {
	if d.pipeline == nil {
		return nil, fmt.Errorf("h264: decoder not initialized")
	}

	if len(encodedData) == 0 {
		return nil, fmt.Errorf("h264: empty encoded data")
	}

	// Create buffer from H264 data
	buffer := gst.NewBufferFromBytes(encodedData)
	if buffer == nil {
		return nil, fmt.Errorf("h264: failed to create GStreamer buffer")
	}
	buffer.SetPresentationTimestamp(d.nextPTS)
	buffer.SetDuration(d.frameDur)
	d.nextPTS += d.frameDur

	// Push to decoder
	ret := d.appsrc.PushBuffer(buffer)
	if ret != gst.FlowOK && ret != gst.FlowFlushing {
		return nil, fmt.Errorf("h264: appsrc push failed: %v", ret)
	}

	// Try to get decoded sample from sink
	sample := d.appsink.TryPullSample(gst.ClockTime((500 * time.Millisecond).Nanoseconds()))
	if sample == nil {
		return nil, fmt.Errorf("h264: no decoded sample available")
	}

	buf := sample.GetBuffer()
	if buf == nil {
		return nil, fmt.Errorf("h264: failed to get buffer from sample")
	}

	// Verify decoded data size
	decodedData := buf.Bytes()
	expectedSize := width * height * 4
	if len(decodedData) != expectedSize {
		return nil, fmt.Errorf("h264: decoded frame size mismatch: got %d, expected %d", len(decodedData), expectedSize)
	}

	return decodedData, nil
}

// CodecName returns "h264"
func (d *H264Decoder) CodecName() string {
	return "h264"
}

// Close stops the decoder and frees resources
func (d *H264Decoder) Close() error {
	if d.pipeline != nil {
		d.pipeline.SetState(gst.StateNull)
	}
	return nil
}

// init registers H264 codec factories with the codec package
func init() {
	codec.RegisterEncoder("h264", func(cfg codec.Config) (codec.Encoder, error) {
		return NewH264Encoder(cfg)
	})
	codec.RegisterDecoder("h264", func(cfg codec.Config) (codec.Decoder, error) {
		return NewH264Decoder(cfg)
	})
}
