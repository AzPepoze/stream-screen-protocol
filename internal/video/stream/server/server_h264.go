package server

import (
	"fmt"
	"sync/atomic"

	videoh264 "streamscreen/internal/video/codec/h264"
	"streamscreen/internal/video/stream"
)

func (s *Sender) EnsureH264Pipeline() error {
	s.cfgMu.RLock()
	if s.codecName != "h264" {
		s.cfgMu.RUnlock()
		return nil
	}
	if s.h264Pipeline != nil {
		s.cfgMu.RUnlock()
		return nil
	}
	codecCfg := make(map[string]interface{}, len(s.cfg.Capture.H264CodecConfig)+1)
	for k, v := range s.cfg.Capture.H264CodecConfig {
		codecCfg[k] = v
	}
	codecCfg["fps"] = s.cfg.Capture.FPS
	s.cfgMu.RUnlock()

	pipeline, err := videoh264.NewServerPipeline(codecCfg)
	if err != nil {
		return fmt.Errorf("failed to initialize h264 pipeline: %w", err)
	}

	// Another goroutine may have initialized/reconfigured the pipeline while
	// the relatively expensive GStreamer construction happened outside cfgMu.
	s.cfgMu.Lock()
	if s.codecName != "h264" {
		s.cfgMu.Unlock()
		_ = pipeline.Close()
		return nil
	}
	if s.h264Pipeline == nil {
		s.h264Pipeline = pipeline
		pipeline = nil
	}
	s.cfgMu.Unlock()
	if pipeline != nil {
		_ = pipeline.Close()
	}
	return nil
}

// SendH264Frame encodes once, packetizes once, then fans the immutable packet
// batch out to independent viewer queues.
func (s *Sender) SendH264Frame(frameData []byte, width, height int) error {
	if s.viewerCount() == 0 {
		return nil
	}
	if err := s.EnsureH264Pipeline(); err != nil {
		return err
	}

	s.cfgMu.RLock()
	pipeline := s.h264Pipeline
	s.cfgMu.RUnlock()
	if pipeline == nil {
		return fmt.Errorf("h264 pipeline unavailable")
	}

	encodedData, err := pipeline.SendFrame(frameData, width, height)
	if err != nil {
		return fmt.Errorf("h264 encoding failed: %w", err)
	}
	if len(encodedData) == 0 {
		return nil
	}

	frameSeq := atomic.AddUint32(&s.frameSeq, 1)
	timestamp := stream.NowTimestampMS()
	totalPackets := uint32((len(encodedData) + stream.CSPMediaPayloadSize - 1) / stream.CSPMediaPayloadSize)
	packets := make([][]byte, 0, totalPackets)
	for packetID := uint32(0); packetID < totalPackets; packetID++ {
		start := packetID * stream.CSPMediaPayloadSize
		end := start + stream.CSPMediaPayloadSize
		if end > uint32(len(encodedData)) {
			end = uint32(len(encodedData))
		}
		payload := encodedData[start:end]
		header := stream.PacketHeader{
			Version:      stream.CSPVersion,
			PacketType:   stream.CSPPacketTypeData,
			FrameSeq:     frameSeq,
			PacketID:     packetID,
			TotalPackets: totalPackets,
			Timestamp:    timestamp,
		}
		buf := make([]byte, stream.CSPHeaderSize+len(payload))
		header.Marshal(buf[:stream.CSPHeaderSize])
		copy(buf[stream.CSPHeaderSize:], payload)
		s.buffer.Put(frameSeq, packetID, buf)
		packets = append(packets, buf)
	}

	s.broadcastH264Batch(packets, frameSeq, h264AccessUnitHasIDR(encodedData))
	return nil
}

// h264AccessUnitHasIDR checks an Annex-B access unit for a type-5 NAL. The
// server uses this only for transport resynchronization: after a viewer queue
// overrun, dependent P frames are skipped until a real decoder recovery point
// can be delivered intact.
func h264AccessUnitHasIDR(data []byte) bool {
	for i := 0; i+4 < len(data); {
		start := -1
		startCodeLen := 0
		if i+3 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			start = i
			startCodeLen = 3
		} else if i+4 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			start = i
			startCodeLen = 4
		}
		if start < 0 {
			i++
			continue
		}
		nal := start + startCodeLen
		if nal < len(data) && data[nal]&0x1f == 5 {
			return true
		}
		i = nal + 1
	}
	return false
}

func (s *Sender) CloseH264Pipeline() error {
	s.cfgMu.Lock()
	pipeline := s.h264Pipeline
	s.h264Pipeline = nil
	s.cfgMu.Unlock()
	if pipeline != nil {
		return pipeline.Close()
	}
	return nil
}
