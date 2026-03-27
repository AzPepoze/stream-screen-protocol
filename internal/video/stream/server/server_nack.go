package server

import (
	"log"
	"net"
	"time"

	"streamscreen/internal/video/stream"
)

func (s *Sender) listenForNACKs() {
	buf := make([]byte, 4096)
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			n, addr, err := s.conn.ReadFromUDP(buf)
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

			switch h.PacketType {
			case stream.CSPPacketTypeJoin:
				log.Printf("Server: received JOIN from %s", addr.String())
				if reported, err := stream.UnmarshalJoin(buf[:n]); err == nil && reported != "" {
					if parsed, err2 := net.ResolveUDPAddr("udp", reported); err2 == nil {
						s.setDestinationAndSeen(parsed)
						log.Printf("Server: using reported endpoint %s (observed %s)", parsed.String(), addr.String())
					} else {
						s.setDestinationAndSeen(addr)
						log.Printf("Server: reported endpoint parse failed (%v), using observed %s", err2, addr.String())
					}
				} else {
					s.setDestinationAndSeen(addr)
					log.Printf("Server: join payload empty, using observed %s", addr.String())
				}
				// Send video info to client only if needed (throttle to every 5 seconds)
				if time.Since(s.lastVideoInfoSent) > 5*time.Second {
					var gridSize int
					if v, ok := s.cfg.Capture.RGBACodecConfig["tile_size"]; ok {
						if val, ok := v.(int); ok {
							gridSize = val
						} else if val, ok := v.(float64); ok {
							gridSize = int(val)
						}
					}
					if gridSize == 0 {
						gridSize = 10
					}
					videoInfoPacket := stream.MarshalVideoInfo(uint32(s.cfg.Capture.Width), uint32(s.cfg.Capture.Height), uint32(s.cfg.Capture.FPS), uint32(gridSize), s.codecName)
					if _, err := s.conn.WriteToUDP(videoInfoPacket, addr); err != nil {
						log.Printf("Server: failed to send VideoInfo to %s: %v (width=%d, height=%d, fps=%d, gridSize=%d, codec=%s)", addr.String(), err, s.cfg.Capture.Width, s.cfg.Capture.Height, s.cfg.Capture.FPS, gridSize, s.codecName)
					} else {
						log.Printf("Server: sent VideoInfo to %s (width=%d, height=%d, fps=%d, gridSize=%d, codec=%s)", addr.String(), s.cfg.Capture.Width, s.cfg.Capture.Height, s.cfg.Capture.FPS, gridSize, s.codecName)
						s.lastVideoInfoSent = time.Now()
					}
				}
				if s.cfg.Audio.Enabled && time.Since(s.lastAudioInfoSent) > 5*time.Second {
					audioInfoPacket := stream.MarshalAudioInfo(
						uint32(s.cfg.Audio.SampleRate),
						uint32(s.cfg.Audio.Channels),
						uint32(s.cfg.Audio.FrameMS),
						uint32(s.cfg.Audio.BitrateKbps),
						s.cfg.Audio.Codec,
					)
					if _, err := s.conn.WriteToUDP(audioInfoPacket, addr); err != nil {
						log.Printf("Server: failed to send AudioInfo to %s: %v", addr.String(), err)
					} else {
						log.Printf("Server: sent AudioInfo to %s (codec=%s sample_rate=%d channels=%d frame_ms=%d bitrate=%dkbps)",
							addr.String(), s.cfg.Audio.Codec, s.cfg.Audio.SampleRate, s.cfg.Audio.Channels, s.cfg.Audio.FrameMS, s.cfg.Audio.BitrateKbps)
						s.lastAudioInfoSent = time.Now()
					}
				}
			case stream.CSPPacketTypeNACK:
				s.setDestinationAndSeen(addr)
				log.Printf("Server: received NACK from %s", addr.String())
			case stream.CSPPacketTypeTileReq:
				s.setDestinationAndSeen(addr)
				tileIDs, err := stream.UnmarshalTileRequest(buf[:n])
				if err == nil {
					log.Printf("Server: received TileRequest from %s for %d tiles", addr.String(), len(tileIDs))
					if s.tileBuffer != nil {
						s.tileBuffer.SetRequestedTiles(tileIDs)
					}
				} else {
					log.Printf("Server: failed to parse TileRequest: %v", err)
				}
			}

			if h.PacketType == stream.CSPPacketTypeNACK {
				frameSeq, ids, err := stream.UnmarshalNACK(buf[:n])
				if err != nil {
					continue
				}

				for _, id := range ids {
					if packet := s.buffer.Get(frameSeq, id); packet != nil {
						_, _ = s.conn.WriteToUDP(packet, addr)
					}
				}
			}
		}
	}
}
