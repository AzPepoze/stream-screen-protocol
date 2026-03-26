package client

import (
	"log"
	"time"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

func (r *ClientReceiver) onNewSample(sink *app.Sink) gst.FlowReturn {
	sample := sink.PullSample()
	if sample == nil {
		return gst.FlowError
	}

	buffer := sample.GetBuffer()
	if buffer == nil {
		return gst.FlowOK
	}

	data := buffer.Bytes()
	r.pixelsMu.Lock()
	defer r.pixelsMu.Unlock()

	// Save current to previous before updating
	copy(r.prevPixels, r.pixels)
	copy(r.pixels, data)
	r.frameSeq++

	return gst.FlowOK
}

// tileFrameReconstructionLoop periodically pushes the frame buffer to GStreamer for display
func (r *ClientReceiver) tileFrameReconstructionLoop() {
	r.videoInfoMu.RLock()
	fps := r.videoFPS
	r.videoInfoMu.RUnlock()

	if fps == 0 {
		fps = 60 // Default FPS
	}

	frameInterval := time.Second / time.Duration(fps)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()

	var timestamp gst.ClockTime = 0
	gstFrameInterval := gst.ClockTime(time.Second) / gst.ClockTime(fps)

	log.Printf("Client: tileFrameReconstructionLoop started - pushing frame buffer to GStreamer at %d fps", fps)

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			if !r.frameDirty.Load() {
				continue
			}

			// Push current frame buffer to GStreamer
			r.frameBufferMu.RLock()
			if len(r.frameBuffer) > 0 {
				// Copy frame buffer to avoid mutation during push
				frameCopy := make([]byte, len(r.frameBuffer))
				copy(frameCopy, r.frameBuffer)
				r.frameBufferMu.RUnlock()

				// Push to GStreamer
				buffer := gst.NewBufferFromBytes(frameCopy)
				buffer.SetPresentationTimestamp(timestamp)
				buffer.SetDuration(gstFrameInterval)

				ret := r.appsrc.PushBuffer(buffer)
				if ret != gst.FlowOK {
					log.Printf("[ERROR] appsrc push returned %v", ret)
				} else {
					r.frameDirty.Store(false)
				}

				timestamp += gstFrameInterval
			} else {
				r.frameBufferMu.RUnlock()
			}
		}
	}
}
