package client

import (
	"log"
	"sync/atomic"
	"time"
)

// appsrcLoop processes frames from frame channel and handles codec-specific decoding
func (r *ClientReceiver) appsrcLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case f, ok := <-r.frameChan:
			if !ok {
				return
			}

			if r.currentCodecName() == "h264" {
				if err := r.HandleH264Frame(f.Data); err != nil {
					r.logH264DecodeError(f.Seq, err)
				}
				continue
			}

			if len(f.Data) == 0 {
				continue
			}
			r.pixelsMu.Lock()
			if len(r.pixels) == len(f.Data) {
				copy(r.prevPixels, r.pixels)
				copy(r.pixels, f.Data)
				r.frameSeq++
			}
			r.pixelsMu.Unlock()
		}
	}
}

// enqueueFrameLatest adds frame to queue, dropping stale frames if full
func (r *ClientReceiver) enqueueFrameLatest(f assembledFrame) {
	select {
	case <-r.ctx.Done():
		return
	case r.frameChan <- f:
		return
	default:
	}

	// Queue full: drop one stale frame then push the latest frame.
	select {
	case <-r.frameChan:
		atomic.AddUint64(&r.ccFrameDrops, 1)
	default:
	}

	select {
	case <-r.ctx.Done():
	case r.frameChan <- f:
	default:
		log.Printf("frame channel full, dropping frame=%d", f.Seq)
		atomic.AddUint64(&r.ccFrameDrops, 1)
	}
}

// logH264DecodeError logs H264 decoding errors with throttling
func (r *ClientReceiver) logH264DecodeError(seq uint32, err error) {
	r.h264ErrMu.Lock()
	defer r.h264ErrMu.Unlock()

	r.h264ErrCount++
	now := time.Now()
	if now.Sub(r.h264ErrLogAt) < time.Second {
		return
	}

	log.Printf("Client: H264 decode errors=%d latest_frame=%d err=%v", r.h264ErrCount, seq, err)
	r.h264ErrLogAt = now
	r.h264ErrCount = 0
}

// compositeTilesWithFallback uses previous frame if current has too much missing data
func (r *ClientReceiver) compositeTilesWithFallback(incomplete []byte) []byte {
	r.pixelsMu.RLock()
	defer r.pixelsMu.RUnlock()

	// Count zeros (indicating data gaps)
	zeroCount := 0
	sampleSize := len(incomplete)
	if sampleSize > 1000 {
		sampleSize = 1000
	}
	for i := 0; i < sampleSize; i++ {
		if incomplete[i] == 0 {
			zeroCount++
		}
	}

	// If more than 20% zeros, use previous frame
	if float64(zeroCount) > float64(sampleSize)*0.2 {
		result := make([]byte, len(incomplete))
		copy(result, r.prevPixels)
		return result
	}

	return incomplete
}
