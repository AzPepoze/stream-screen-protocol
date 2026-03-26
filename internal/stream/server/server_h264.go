package server

import (
	"fmt"
	"streamscreen/internal/codec/h264"
)

// SendH264Frame encodes a raw RGBA frame to H264 and sends as data packets
func (s *Sender) SendH264Frame(frameData []byte, width, height int) error {
	destAddr := s.activeDestination()
	if destAddr == nil {
		return nil // No destination, don't send
	}

	// Create pipeline on first use
	if s.h264Pipeline == nil {
		codecCfg := make(map[string]interface{}, len(s.cfg.CodecConfig)+1)
		for k, v := range s.cfg.CodecConfig {
			codecCfg[k] = v
		}
		codecCfg["fps"] = s.cfg.Capture.FPS
		pipeline, err := h264.NewServerPipeline(codecCfg)
		if err != nil {
			return fmt.Errorf("failed to initialize h264 pipeline: %w", err)
		}
		s.h264Pipeline = pipeline
	}

	// Encode frame
	encodedData, err := s.h264Pipeline.SendFrame(frameData, width, height)
	if err != nil {
		return fmt.Errorf("h264 encoding failed: %w", err)
	}

	// Increment frame sequence
	s.frameSeq++

	// Fragment and send encoded data
	_, err = s.h264Pipeline.FragmentAndSend(encodedData, s.frameSeq, func(buf []byte) error {
		_, err := s.conn.WriteToUDP(buf, destAddr)
		return err
	})

	return err
}

// CloseH264Pipeline stops the H264 pipeline
func (s *Sender) CloseH264Pipeline() error {
	if s.h264Pipeline != nil {
		return s.h264Pipeline.Close()
	}
	return nil
}
