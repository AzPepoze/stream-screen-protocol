package server

import (
	"net"
	"time"

	"streamscreen/internal/logger"
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
				target := s.resolveJoinEndpoint(addr, buf[:n])
				s.registerViewer(target)
				s.sendSessionInfo(target)
				logger.Info("[server] viewer joined %s active=%d", target.String(), s.viewerCount())

			case stream.CSPPacketTypeNACK:
				s.touchViewer(addr)
				frameSeq, ids, err := stream.UnmarshalNACK(buf[:n])
				if err != nil {
					continue
				}
				repair := make([][]byte, 0, len(ids))
				for _, id := range ids {
					packet := s.buffer.Get(frameSeq, id)
					if packet == nil || !s.packetBeforeDeadline(packet) {
						continue
					}
					repair = append(repair, packet)
				}
				if len(repair) > 0 {
					s.enqueueRepair(addr, repair, frameSeq)
				}

			case stream.CSPPacketTypeTileReq:
				s.touchViewer(addr)
				tileIDs, err := stream.UnmarshalTileRequest(buf[:n])
				if err != nil || s.blockyPipeline == nil {
					continue
				}
				packets, err := s.blockyPipeline.BuildTilesBatch(s.frameSeq, tileIDs, stream.NowTimestampMS())
				if err == nil && len(packets) > 0 {
					s.enqueueRepair(addr, packets, s.frameSeq)
				}

			case stream.CSPPacketTypeControl:
				feedback, err := stream.UnmarshalExtendedControlFeedback(buf[:n])
				if err != nil {
					logger.Info("[server] invalid control feedback from %s: %v", addr.String(), err)
					continue
				}
				s.applyControlFeedback(addr, feedback)

			case stream.CSPPacketTypeProbe:
				s.touchViewer(addr)
				_, _ = s.conn.WriteToUDP(stream.MarshalProbeReply(h.FrameSeq, h.Timestamp), addr)
			}
		}
	}
}

func (s *Sender) resolveJoinEndpoint(observed *net.UDPAddr, packet []byte) *net.UDPAddr {
	reported, err := stream.UnmarshalJoin(packet)
	if err != nil || reported == "" {
		return observed
	}
	parsed, err := net.ResolveUDPAddr("udp", reported)
	if err != nil {
		return observed
	}
	// A reported endpoint is only trusted when it refers to the same observed
	// IP. This avoids accidentally replacing a NAT-mapped address with an
	// unrelated endpoint while retaining the existing explicit-port feature.
	if !parsed.IP.Equal(observed.IP) {
		return observed
	}
	return parsed
}

func (s *Sender) sendSessionInfo(addr *net.UDPAddr) {
	if addr == nil {
		return
	}
	gridSize := 10
	if v, ok := s.cfg.Capture.RGBACodecConfig["tile_size"]; ok {
		if val, ok := v.(int); ok {
			gridSize = val
		} else if val, ok := v.(float64); ok {
			gridSize = int(val)
		}
	}
	videoInfo := stream.MarshalVideoInfo(
		uint32(s.cfg.Capture.Width),
		uint32(s.cfg.Capture.Height),
		uint32(s.cfg.Capture.FPS),
		uint32(gridSize),
		s.codecName,
	)
	_, _ = s.conn.WriteToUDP(videoInfo, addr)

	if s.cfg.Audio.Enabled {
		audioInfo := stream.MarshalAudioInfo(
			uint32(s.cfg.Audio.SampleRate),
			uint32(s.cfg.Audio.Channels),
			uint32(s.cfg.Audio.FrameMS),
			uint32(s.cfg.Audio.BitrateKbps),
			s.cfg.Audio.Codec,
		)
		_, _ = s.conn.WriteToUDP(audioInfo, addr)
	}
}

func (s *Sender) packetBeforeDeadline(packet []byte) bool {
	if len(packet) < stream.CSPHeaderSize {
		return false
	}
	var h stream.PacketHeader
	if err := h.Unmarshal(packet[:stream.CSPHeaderSize]); err != nil {
		return false
	}
	if h.Timestamp == 0 {
		return true
	}
	age := time.Duration(stream.TimestampAgeMS(h.Timestamp)) * time.Millisecond
	return age <= s.frameDeadline
}
