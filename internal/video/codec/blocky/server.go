package blocky

import (
	"fmt"
	"net"
	"time"

	"streamscreen/internal/logger"
	"streamscreen/internal/video/stream"
)

// TileGetter is an interface for getting tile data.
type TileGetter interface {
	GetTile(tileID uint16) []byte
}

// ServerPipeline handles RGBA tile-based delta encoding and transmission.
type ServerPipeline struct {
	tileBuffer TileGetter
	config     map[string]interface{}
}

func NewServerPipeline(tileBuffer TileGetter, cfg map[string]interface{}) *ServerPipeline {
	if cfg == nil {
		cfg = make(map[string]interface{})
	}
	return &ServerPipeline{tileBuffer: tileBuffer, config: cfg}
}

// BuildTilesBatch packetizes a set of tiles once. The returned byte slices are
// immutable and can be shared across multiple viewer send queues.
func (p *ServerPipeline) BuildTilesBatch(frameSeq uint32, tileIDs []uint16, timestamp uint32) ([][]byte, error) {
	if len(tileIDs) == 0 || p.tileBuffer == nil {
		return nil, nil
	}

	packets := make([][]byte, 0, len(tileIDs))
	for _, tileID := range tileIDs {
		tileData := p.tileBuffer.GetTile(tileID)
		if len(tileData) == 0 {
			continue
		}

		tilePacket := stream.MarshalTile(frameSeq, tileID, tileData)
		totalTileFragments := uint32((len(tilePacket) + stream.CSPMaxPayloadSize - 1) / stream.CSPMaxPayloadSize)
		if totalTileFragments == 0 {
			continue
		}

		for offset, packetID := uint32(0), uint32(0); offset < uint32(len(tilePacket)); offset += uint32(stream.CSPMaxPayloadSize) {
			end := offset + uint32(stream.CSPMaxPayloadSize)
			if end > uint32(len(tilePacket)) {
				end = uint32(len(tilePacket))
			}
			payload := tilePacket[offset:end]
			packet := make([]byte, stream.CSPHeaderSize+len(payload))
			header := stream.PacketHeader{
				Version:      stream.CSPVersion,
				PacketType:   stream.CSPPacketTypeTile,
				TileID:       tileID,
				FrameSeq:     frameSeq,
				PacketID:     packetID,
				TotalPackets: totalTileFragments,
				Timestamp:    timestamp,
			}
			header.Marshal(packet[:stream.CSPHeaderSize])
			copy(packet[stream.CSPHeaderSize:], payload)
			packets = append(packets, packet)
			packetID++
		}
	}
	return packets, nil
}

func (p *ServerPipeline) SendTilesBurst(frameSeq uint32, tileIDs []uint16, conn *net.UDPConn, destAddr *net.UDPAddr) error {
	return p.SendTilesBurstWithPacing(frameSeq, tileIDs, conn, destAddr, 0)
}

func (p *ServerPipeline) SendTilesBurstWithPacing(frameSeq uint32, tileIDs []uint16, conn *net.UDPConn, destAddr *net.UDPAddr, packetGap time.Duration) error {
	if conn == nil || destAddr == nil {
		return nil
	}
	packets, err := p.BuildTilesBatch(frameSeq, tileIDs, stream.NowTimestampMS())
	if err != nil {
		return err
	}
	for _, packet := range packets {
		if _, err := conn.WriteToUDP(packet, destAddr); err != nil {
			logger.Info("[blocky-server] write error frame=%d dest=%s err=%v", frameSeq, destAddr.String(), err)
			return fmt.Errorf("blocky write: %w", err)
		}
		if packetGap > 0 {
			time.Sleep(packetGap)
		}
	}
	return nil
}

func (p *ServerPipeline) Close() error { return nil }
