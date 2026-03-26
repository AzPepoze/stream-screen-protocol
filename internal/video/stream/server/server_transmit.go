package server

import (
	"log"

	"streamscreen/internal/video/stream"
)

func (s *Sender) transmitFrame(data []byte) {
	s.destAddrMu.RLock()
	dest := s.destAddr
	s.destAddrMu.RUnlock()

	if dest == nil {
		log.Printf("Server: no client discovered, dropping frame")
		return // No client discovered yet
	}

	s.frameSeq++
	timestamp := uint32(0)

	totalPackets := uint32((len(data) + stream.CSPMaxPayloadSize - 1) / stream.CSPMaxPayloadSize)

	log.Printf("Server: transmit frame=%d totalPackets=%d size=%d dest=%s", s.frameSeq, totalPackets, len(data), dest.String())

	// Build all packets first, then send in burst
	packets := make([][]byte, totalPackets)
	for i := uint32(0); i < totalPackets; i++ {
		start := i * stream.CSPMaxPayloadSize
		end := start + stream.CSPMaxPayloadSize
		if end > uint32(len(data)) {
			end = uint32(len(data))
		}

		payload := data[start:end]
		packet := make([]byte, stream.CSPHeaderSize+len(payload))

		header := stream.PacketHeader{
			Version:      stream.CSPVersion,
			PacketType:   stream.CSPPacketTypeData,
			FrameSeq:     s.frameSeq,
			PacketID:     i,
			TotalPackets: totalPackets,
			Timestamp:    timestamp,
		}
		header.Marshal(packet[:stream.CSPHeaderSize])
		copy(packet[stream.CSPHeaderSize:], payload)

		// Store in retransmit buffer
		s.buffer.Put(s.frameSeq, i, packet)
		packets[i] = packet
	}

	// Burst send all packets with minimal delay
	sentCount := 0
	for _, packet := range packets {
		_, err := s.conn.WriteToUDP(packet, dest)
		if err != nil {
			log.Printf("Server: write error frame=%d dest=%s err=%v", s.frameSeq, dest.String(), err)
		} else {
			sentCount++
		}
	}
}
