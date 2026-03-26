package h264

import (
	"fmt"
	"log"
	"streamscreen/internal/codec"
	"streamscreen/internal/stream"
)

// ServerPipeline handles H264 encoding and transmission
type ServerPipeline struct {
	encoder codec.Encoder
	config  codec.Config
}

// NewServerPipeline creates a new H264 server pipeline
func NewServerPipeline(cfg codec.Config) (*ServerPipeline, error) {
	h264Enc, err := codec.NewEncoder("h264", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create h264 encoder: %w", err)
	}
	return &ServerPipeline{
		encoder: h264Enc,
		config:  cfg,
	}, nil
}

// SendFrame encodes a raw RGBA frame to H264 and returns the encoded data for transmission
func (p *ServerPipeline) SendFrame(frameData []byte, width, height int) ([]byte, error) {
	if p.encoder == nil {
		return nil, fmt.Errorf("encoder not initialized")
	}

	encodedData, err := p.encoder.Encode(frameData, width, height)
	if err != nil {
		return nil, fmt.Errorf("h264 encoding failed: %w", err)
	}

	return encodedData, nil
}

// FragmentAndSend fragments encoded data into packets and sends them
func (p *ServerPipeline) FragmentAndSend(encodedData []byte, frameSeq uint32, sendFunc func(buf []byte) error) (uint32, error) {
	const maxPayloadSize = stream.CSPMaxPayloadSize
	totalPackets := uint32((len(encodedData) + maxPayloadSize - 1) / maxPayloadSize)

	for packetID := uint32(0); packetID < totalPackets; packetID++ {
		start := packetID * maxPayloadSize
		end := start + maxPayloadSize
		if end > uint32(len(encodedData)) {
			end = uint32(len(encodedData))
		}

		payload := encodedData[start:end]

		// Create packet header
		header := stream.PacketHeader{
			Version:      stream.CSPVersion,
			PacketType:   stream.CSPPacketTypeData,
			FrameSeq:     frameSeq,
			PacketID:     packetID,
			TotalPackets: totalPackets,
		}

		// Marshal packet
		buf := make([]byte, stream.CSPHeaderSize+len(payload))
		header.Marshal(buf[:stream.CSPHeaderSize])
		copy(buf[stream.CSPHeaderSize:], payload)

		// Send packet
		if err := sendFunc(buf); err != nil {
			log.Printf("Server: failed to send H264 packet %d/%d: %v", packetID+1, totalPackets, err)
			return frameSeq, err
		}
	}

	log.Printf("Server: sent H264 frame %d (%.1f KB in %d packets)", frameSeq, float64(len(encodedData))/1024, totalPackets)
	return frameSeq, nil
}

// Close stops the encoder
func (p *ServerPipeline) Close() error {
	if p.encoder != nil {
		return p.encoder.Close()
	}
	return nil
}
