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
	if err == nil {
		logger.Info("Client: VideoInfo - %dx%d @ %d fps, gridSize=%d, codec=%s", w, ht, fps, gridSize, codecName)
		r.videoInfoMu.Lock()
		r.videoWidth = w
		r.videoHeight = ht
		r.videoFPS = fps
		r.tileGridSize = int(gridSize)
		r.codecName = codecName
		r.videoInfoMu.Unlock()
	}
}

func (r *ClientReceiver) handleAudioInfo(buf []byte, n int) {
	sampleRate, channels, frameMS, bitrate, codecName, err := stream.UnmarshalAudioInfo(buf[:n])
	if err == nil {
		r.setAudioInfo(sampleRate, channels, frameMS, bitrate, codecName)
		logger.Info("Client: AudioInfo - codec=%s sample_rate=%d channels=%d frame_ms=%d bitrate=%dkbps",
			codecName, sampleRate, channels, frameMS, bitrate)
	}
}
