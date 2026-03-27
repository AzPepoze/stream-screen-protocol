package client

import (
	"log"
	"streamscreen/internal/video/stream"
)

// receiveLoop reads packets from UDP connection and routes them to handlers
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

			// Route packet based on type
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
				if data, seq := r.jitterBuffer.Push(h, payload); data != nil {
					r.enqueueFrameLatest(assembledFrame{Seq: seq, Data: data})
				}
			}
		}
	}
}

// handleVideoInfo processes VideoInfo packets from server
func (r *ClientReceiver) handleVideoInfo(buf []byte, n int) {
	w, ht, fps, gridSize, codecName, err := stream.UnmarshalVideoInfo(buf[:n])
	if err == nil {
		log.Printf("Client: VideoInfo - %dx%d @ %d fps, gridSize=%d, codec=%s", w, ht, fps, gridSize, codecName)
		r.videoInfoMu.Lock()
		r.videoWidth = w
		r.videoHeight = ht
		r.videoFPS = fps
		r.tileGridSize = int(gridSize)
		r.codecName = codecName
		r.videoInfoMu.Unlock()
	}
}

// handleAudioInfo processes AudioInfo packets from server
func (r *ClientReceiver) handleAudioInfo(buf []byte, n int) {
	sampleRate, channels, frameMS, bitrate, codecName, err := stream.UnmarshalAudioInfo(buf[:n])
	if err == nil {
		r.setAudioInfo(sampleRate, channels, frameMS, bitrate, codecName)
		log.Printf("Client: AudioInfo - codec=%s sample_rate=%d channels=%d frame_ms=%d bitrate=%dkbps",
			codecName, sampleRate, channels, frameMS, bitrate)
	}
}
