package client

import (
	"log"
	"time"
)

// tileFrameReconstructionLoop periodically composites tile updates into display pixels.
func (r *ClientReceiver) tileFrameReconstructionLoop() {
	r.videoInfoMu.RLock()
	fps := r.videoFPS
	r.videoInfoMu.RUnlock()

	if fps == 0 {
		fps = 60
	}

	frameInterval := time.Second / time.Duration(fps)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()

	log.Printf("Client: tileFrameReconstructionLoop started - compositing at %d fps", fps)

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			if !r.frameDirty.Load() {
				continue
			}

			r.frameBufferMu.RLock()
			if len(r.frameBuffer) == 0 {
				r.frameBufferMu.RUnlock()
				continue
			}
			frameCopy := make([]byte, len(r.frameBuffer))
			copy(frameCopy, r.frameBuffer)
			r.frameBufferMu.RUnlock()

			r.pixelsMu.Lock()
			if len(r.pixels) == len(frameCopy) {
				copy(r.prevPixels, r.pixels)
				copy(r.pixels, frameCopy)
				r.frameSeq++
				r.frameDirty.Store(false)
			}
			r.pixelsMu.Unlock()
		}
	}
}
