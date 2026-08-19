package client

import (
	"sync/atomic"

	"streamscreen/internal/logger"
	"streamscreen/internal/video/stream"
)

// receiveLoop reads packets from UDP connection and routes them to handlers.
func (r *ClientReceiver) receiveLoop() {
	buf := make([]byte, 65535)
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
			n, _, err := r.conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			if n < stream.CSPHeaderSize {
				continue
			}

			var h stream.PacketHeader
			if err := h.Unmarshal(buf[:stream.CSPHeaderSize]); err != nil {
				continue
			}
			atomic.AddUint64(&r.ccPacketsReceived, 1)
			atomic.AddUint64(&r.ccBytesReceived, uint64(n))

			switch h.PacketType {
			case stream.CSPPacketTypeVideoInfo:
				r.handleVideoInfo(buf, n)
			case stream.CSPPacketTypeTile:
				r.handleTilePacket(h, buf[:n])
			case stream.CSPPacketTypeAudioInfo:
				r.handleAudioInfo(buf, n)
			case stream.CSPPacketTypeAudioData:
				r.handleAudioDataPacket(h.FrameSeq, h.PacketID, h.TotalPackets, buf[stream.CSPHeaderSize:n])
			case stream.CSPPacketTypeData:
				payload := buf[stream.CSPHeaderSize:n]
				recovered := r.fecRecoverer.PushData(h, payload)
				r.pushVideoPacket(h, payload)
				r.pushRecoveredPackets(recovered)
			case stream.CSPPacketTypeFEC:
				recovered, err := r.fecRecoverer.PushFEC(buf[:n])
				if err == nil {
					r.pushRecoveredPackets(recovered)
				}
			case stream.CSPPacketTypeProbeReply:
				rtt := stream.TimestampAgeMS(h.Timestamp)
				atomic.StoreUint32(&r.ccRTTMS, rtt)
			}
		}
	}
}

func (r *ClientReceiver) pushRecoveredPackets(packets []recoveredPacket) {
	if len(packets) == 0 {
		return
	}
	atomic.AddUint64(&r.ccFECRecovered, uint64(len(packets)))
	for _, packet := range packets {
		r.pushVideoPacket(packet.Header, packet.Payload)
	}
}

func (r *ClientReceiver) pushVideoPacket(h stream.PacketHeader, payload []byte) {
	if data, seq := r.jitterBuffer.Push(h, payload); data != nil {
		r.fecRecoverer.ForgetFrame(seq)
		r.enqueueFrameLatest(assembledFrame{Seq: seq, Data: data})
	}
}

func (r *ClientReceiver) handleVideoInfo(buf []byte, n int) {
	w, ht, fps, gridSize, codecName, err := stream.UnmarshalVideoInfo(buf[:n])
	if err != nil {
		return
	}
	if codecName == "" {
		codecName = "rgba"
	}

	r.videoInfoMu.Lock()
	changed := (r.videoWidth != w || r.videoHeight != ht || r.videoFPS != fps || r.tileGridSize != int(gridSize) || r.codecName != codecName)
	oldCodec := r.codecName
	wasInitialized := r.videoWidth > 0 && r.videoHeight > 0

	r.videoWidth = w
	r.videoHeight = ht
	r.videoFPS = fps
	r.tileGridSize = int(gridSize)
	r.codecName = codecName
	r.videoInfoMu.Unlock()

	if changed && wasInitialized {
		logger.Info("client", "VideoInfo updated - %dx%d @ %d fps, gridSize=%d, codec=%s (was %s)",
			w, ht, fps, gridSize, codecName, oldCodec)

		r.applyJitterTimingFromFPS(int(fps))

		pixelSize := int(w * ht * 4)
		r.pixelsMu.Lock()
		if len(r.pixels) != pixelSize {
			r.pixels = make([]byte, pixelSize)
			r.prevPixels = make([]byte, pixelSize)
		} else {
			for i := range r.pixels {
				r.pixels[i] = 0
			}
			for i := range r.prevPixels {
				r.prevPixels[i] = 0
			}
		}
		r.pixelsMu.Unlock()

		r.frameBufferMu.Lock()
		r.frameBuffer = make([]byte, pixelSize)
		r.frameBufferMu.Unlock()

		r.tileGrid = NewTileGrid(int(gridSize), int(w), int(ht))

		if codecName == "h264" {
			if oldCodec != "h264" || r.h264Pipeline == nil {
				_ = r.CloseH264Pipeline()
				_ = r.ensureH264Pipeline()
			}
			r.jitterBuffer.SetCompleteFramesOnly()
		} else {
			_ = r.CloseH264Pipeline()
			r.jitterBuffer.SetAllowPartial(r.cfg.Network.AllowPartial, r.cfg.Network.ForceOutput)
		}

		r.jitterBuffer.Flush()
		r.flushFrames()
		atomic.StoreUint32(&r.canvasRefreshFlag, 1)
	}
}

func (r *ClientReceiver) handleAudioInfo(buf []byte, n int) {
	sampleRate, channels, frameMS, bitrate, codecName, err := stream.UnmarshalAudioInfo(buf[:n])
	if err == nil {
		r.setAudioInfo(sampleRate, channels, frameMS, bitrate, codecName)
		logger.Info("audio", "AudioInfo - codec=%s sample_rate=%d channels=%d frame_ms=%d bitrate=%dkbps",
			codecName, sampleRate, channels, frameMS, bitrate)
	}
}
