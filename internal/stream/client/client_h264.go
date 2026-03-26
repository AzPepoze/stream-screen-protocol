package client

import (
	"fmt"
	"streamscreen/internal/codec/h264"
)

// HandleH264Frame decodes H264 packet data and stores decoded RGBA
func (r *ClientReceiver) HandleH264Frame(h264Data []byte) error {
	// Initialize pipeline on first use
	if r.h264Pipeline == nil {
		r.videoInfoMu.RLock()
		fps := int(r.videoFPS)
		r.videoInfoMu.RUnlock()
		if fps <= 0 {
			fps = 60
		}
		pipeline, err := h264.NewClientPipeline(map[string]interface{}{
			"fps": fps,
		})
		if err != nil {
			return fmt.Errorf("failed to initialize h264 pipeline: %w", err)
		}
		r.h264Pipeline = pipeline
	}

	// Get video dimensions
	r.videoInfoMu.RLock()
	width := int(r.videoWidth)
	height := int(r.videoHeight)
	r.videoInfoMu.RUnlock()

	if width == 0 || height == 0 {
		return fmt.Errorf("video dimensions not set")
	}

	// Decode H264 frame to RGBA
	rgbaData, err := r.h264Pipeline.HandleFrame(h264Data, width, height)
	if err != nil {
		return err
	}

	// Store decoded frame
	r.pixelsMu.Lock()
	defer r.pixelsMu.Unlock()

	// Save current to previous before updating
	copy(r.prevPixels, rgbaData)
	copy(r.pixels, rgbaData)
	r.frameSeq++

	return nil
}

// CloseH264Pipeline stops the H264 pipeline
func (r *ClientReceiver) CloseH264Pipeline() error {
	if r.h264Pipeline != nil {
		return r.h264Pipeline.Close()
	}
	return nil
}
