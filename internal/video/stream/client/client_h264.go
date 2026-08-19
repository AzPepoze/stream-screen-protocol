package client

import (
	"fmt"
	"sync/atomic"

	videoh264 "streamscreen/internal/video/codec/h264"
)

// HandleH264Frame pushes H264 packet data into the decoder asynchronously
func (r *ClientReceiver) HandleH264Frame(h264Data []byte) error {
	if err := r.ensureH264Pipeline(); err != nil {
		return err
	}

	// Get video dimensions
	r.videoInfoMu.RLock()
	width := int(r.videoWidth)
	height := int(r.videoHeight)
	r.videoInfoMu.RUnlock()

	if width == 0 || height == 0 {
		return fmt.Errorf("video dimensions not set")
	}

	// Push H264 frame non-blockingly into the decoder
	return r.h264Pipeline.PushFrame(h264Data, width, height)
}

func (r *ClientReceiver) ensureH264Pipeline() error {
	if r.h264Pipeline != nil {
		return nil
	}
	r.videoInfoMu.RLock()
	fps := int(r.videoFPS)
	r.videoInfoMu.RUnlock()
	if fps <= 0 {
		fps = 60
	}
	codecCfg := make(map[string]interface{}, len(r.cfg.CodecConfig)+1)
	for k, v := range r.cfg.CodecConfig {
		codecCfg[k] = v
	}
	codecCfg["fps"] = fps
	pipeline, err := videoh264.NewClientPipeline(codecCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize h264 pipeline: %w", err)
	}

	r.h264DecodedFrames = make(chan []byte, 2)
	pipeline.SetOutputHandler(func(rgbaData []byte, width, height int) {
		expectedSize := width * height * 4
		if len(rgbaData) != expectedSize {
			return
		}
		select {
		case r.h264DecodedFrames <- rgbaData:
		default:
			// Queue full: drop oldest frame to maintain lowest latency and latest-frame semantics
			select {
			case <-r.h264DecodedFrames:
				atomic.AddUint64(&r.ccDecodedDrops, 1)
			default:
			}
			select {
			case r.h264DecodedFrames <- rgbaData:
			default:
				atomic.AddUint64(&r.ccDecodedDrops, 1)
			}
		}
	})

	r.h264Pipeline = pipeline
	return nil
}

// h264OutputLoop consumes decoded RGBA frames asynchronously on a dedicated client-owned loop
func (r *ClientReceiver) h264OutputLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case rgbaData, ok := <-r.h264DecodedFrames:
			if !ok {
				return
			}
			r.pixelsMu.Lock()
			if len(r.pixels) == len(rgbaData) {
				copy(r.prevPixels, r.pixels)
				copy(r.pixels, rgbaData)
				r.frameSeq++
				atomic.AddUint64(&r.ccDecodedFrames, 1)
			}
			r.pixelsMu.Unlock()
		}
	}
}

// CloseH264Pipeline stops the H264 pipeline
func (r *ClientReceiver) CloseH264Pipeline() error {
	if r.h264Pipeline != nil {
		err := r.h264Pipeline.Close()
		r.h264Pipeline = nil
		return err
	}
	return nil
}
