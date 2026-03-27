package server

import (
	"fmt"

	videoh264 "streamscreen/internal/video/codec/h264"
	"streamscreen/internal/video/stream"
)

func (s *Sender) EnsureH264Pipeline() error {
	if s.codecName != "h264" {
		return nil
	}
	if s.h264Pipeline != nil {
		return nil
	}
	codecCfg := make(map[string]interface{}, len(s.cfg.Capture.H264CodecConfig)+1)
	for k, v := range s.cfg.Capture.H264CodecConfig {
		codecCfg[k] = v
	}
	codecCfg["fps"] = s.cfg.Capture.FPS
	pipeline, err := videoh264.NewServerPipeline(codecCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize h264 pipeline: %w", err)
	}
	s.h264Pipeline = pipeline
	return nil
}

// SendH264Frame encodes a raw RGBA frame to H264 and sends as data packets.
func (s *Sender) SendH264Frame(frameData []byte, width, height int) error {
	destAddr := s.activeDestination()
	if destAddr == nil {
		return nil // No destination, don't send
	}

	if err := s.EnsureH264Pipeline(); err != nil {
		return err
	}

	encodedData, err := s.h264Pipeline.SendFrame(frameData, width, height)
	if err != nil {
		return fmt.Errorf("h264 encoding failed: %w", err)
	}

	s.frameSeq++
	totalPackets := uint32((len(encodedData) + stream.CSPMaxPayloadSize - 1) / stream.CSPMaxPayloadSize)
	for packetID := uint32(0); packetID < totalPackets; packetID++ {
		start := packetID * stream.CSPMaxPayloadSize
		end := start + stream.CSPMaxPayloadSize
		if end > uint32(len(encodedData)) {
			end = uint32(len(encodedData))
		}

		payload := encodedData[start:end]
		header := stream.PacketHeader{
			Version:      stream.CSPVersion,
			PacketType:   stream.CSPPacketTypeData,
			FrameSeq:     s.frameSeq,
			PacketID:     packetID,
			TotalPackets: totalPackets,
		}

		buf := make([]byte, stream.CSPHeaderSize+len(payload))
		header.Marshal(buf[:stream.CSPHeaderSize])
		copy(buf[stream.CSPHeaderSize:], payload)
		s.buffer.Put(s.frameSeq, packetID, buf)
		if _, err := s.conn.WriteToUDP(buf, destAddr); err != nil {
			return err
		}
	}

	return nil
}

// CloseH264Pipeline stops the H264 pipeline.
func (s *Sender) CloseH264Pipeline() error {
	if s.h264Pipeline != nil {
		return s.h264Pipeline.Close()
	}
	return nil
}
