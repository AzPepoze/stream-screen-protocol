package server

import (
	"sync/atomic"

	"streamscreen/internal/video/stream"
)

// transmitFrame is retained for generic encoded-frame call sites. It now
// packetizes once and uses the same multi-viewer fan-out path as H.264.
func (s *Sender) transmitFrame(data []byte) {
	if len(data) == 0 || s.viewerCount() == 0 {
		return
	}

	frameSeq := atomic.AddUint32(&s.frameSeq, 1)
	timestamp := stream.NowTimestampMS()
	totalPackets := uint32((len(data) + stream.CSPMediaPayloadSize - 1) / stream.CSPMediaPayloadSize)
	packets := make([][]byte, 0, totalPackets)
	for i := uint32(0); i < totalPackets; i++ {
		start := i * stream.CSPMediaPayloadSize
		end := start + stream.CSPMediaPayloadSize
		if end > uint32(len(data)) {
			end = uint32(len(data))
		}
		payload := data[start:end]
		packet := make([]byte, stream.CSPHeaderSize+len(payload))
		header := stream.PacketHeader{
			Version:      stream.CSPVersion,
			PacketType:   stream.CSPPacketTypeData,
			FrameSeq:     frameSeq,
			PacketID:     i,
			TotalPackets: totalPackets,
			Timestamp:    timestamp,
		}
		header.Marshal(packet[:stream.CSPHeaderSize])
		copy(packet[stream.CSPHeaderSize:], payload)
		s.buffer.Put(frameSeq, i, packet)
		packets = append(packets, packet)
	}
	s.broadcastVideoBatch(packets, frameSeq)
}
