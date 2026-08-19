package server

import (
	"context"

	"streamscreen/internal/audio/capture"
	"streamscreen/internal/audio/opus"
	"streamscreen/internal/logger"
	"streamscreen/internal/video/stream"
)

func (s *Sender) StartAudio() error {
	if !s.cfg.Audio.Enabled {
		return nil
	}
	if s.audioCancel != nil {
		return nil
	}

	source, err := capture.New(s.cfg)
	if err != nil {
		return err
	}
	encoder, err := opus.NewEncoder(s.cfg)
	if err != nil {
		_ = source.Close()
		return err
	}

	audioCtx, cancel := context.WithCancel(s.ctx)
	s.audioCancel = cancel
	if err := source.Start(audioCtx); err != nil {
		_ = encoder.Close()
		_ = source.Close()
		cancel()
		s.audioCancel = nil
		return err
	}

	logger.Info("audio", "audio pipeline started codec=%s sample_rate=%d channels=%d frame_ms=%d", s.cfg.Audio.Codec, s.cfg.Audio.SampleRate, s.cfg.Audio.Channels, s.cfg.Audio.FrameMS)

	go func() {
		defer func() {
			_ = encoder.Close()
			_ = source.Close()
		}()

		audioSeq := uint32(0)
		for {
			select {
			case <-audioCtx.Done():
				return
			case pcm, ok := <-source.Frames():
				if !ok {
					return
				}
				if s.viewerCount() == 0 {
					continue
				}

				encoded, err := encoder.EncodePCM(pcm)
				if err != nil {
					continue
				}
				audioSeq++
				totalPackets := uint32((len(encoded) + stream.CSPMaxPayloadSize - 1) / stream.CSPMaxPayloadSize)
				if totalPackets == 0 {
					continue
				}

				timestamp := stream.NowTimestampMS()
				packets := make([][]byte, 0, totalPackets)
				for packetID := uint32(0); packetID < totalPackets; packetID++ {
					start := packetID * stream.CSPMaxPayloadSize
					end := start + stream.CSPMaxPayloadSize
					if end > uint32(len(encoded)) {
						end = uint32(len(encoded))
					}
					payload := encoded[start:end]
					header := stream.PacketHeader{
						Version:      stream.CSPVersion,
						PacketType:   stream.CSPPacketTypeAudioData,
						FrameSeq:     audioSeq,
						PacketID:     packetID,
						TotalPackets: totalPackets,
						Timestamp:    timestamp,
					}
					buf := make([]byte, stream.CSPHeaderSize+len(payload))
					header.Marshal(buf[:stream.CSPHeaderSize])
					copy(buf[stream.CSPHeaderSize:], payload)
					packets = append(packets, buf)
				}
				s.broadcastAudioBatch(packets, audioSeq)
			}
		}
	}()

	return nil
}
