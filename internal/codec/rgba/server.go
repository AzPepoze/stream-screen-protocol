package rgba

import (
	"log"
	"net"
	"streamscreen/internal/stream"
)

// TileGetter is an interface for getting tile data
type TileGetter interface {
	GetTile(tileID uint16) []byte
}

// ServerPipeline handles RGBA tile-based delta encoding and transmission
type ServerPipeline struct {
	tileBuffer TileGetter
	config     map[string]interface{}
}

// NewServerPipeline creates a new RGBA server pipeline
func NewServerPipeline(tileBuffer TileGetter, cfg map[string]interface{}) *ServerPipeline {
	if cfg == nil {
		cfg = make(map[string]interface{})
	}
	return &ServerPipeline{
		tileBuffer: tileBuffer,
		config:     cfg,
	}
}

// SendTilesBurst sends all tiles in a single burst with per-tile fragmentation tracking
func (p *ServerPipeline) SendTilesBurst(frameSeq uint32, tileIDs []uint16, conn *net.UDPConn, destAddr *net.UDPAddr) error {
	if len(tileIDs) == 0 {
		return nil
	}

	if p.tileBuffer == nil || conn == nil || destAddr == nil {
		return nil
	}

	sentCount := 0

	// Send each tile with its own fragment count
	for _, tileID := range tileIDs {
		tileData := p.tileBuffer.GetTile(tileID)
		if tileData == nil || len(tileData) == 0 {
			continue
		}

		// Create tile packet
		tilePacket := stream.MarshalTile(frameSeq, tileID, tileData)

		// Calculate fragments for this specific tile only
		totalTileFragments := uint32((len(tilePacket) + stream.CSPMaxPayloadSize - 1) / stream.CSPMaxPayloadSize)

		// Fragment this tile (each fragment numbered 0..N for THIS tile)
		for offset, packetID := uint32(0), uint32(0); offset < uint32(len(tilePacket)); offset += uint32(stream.CSPMaxPayloadSize) {
			end := offset + uint32(stream.CSPMaxPayloadSize)
			if end > uint32(len(tilePacket)) {
				end = uint32(len(tilePacket))
			}

			payload := tilePacket[offset:end]
			packet := make([]byte, stream.CSPHeaderSize+len(payload))

			// TotalPackets = fragments for THIS tile only (not frame total)
			header := stream.PacketHeader{
				Version:      stream.CSPVersion,
				PacketType:   stream.CSPPacketTypeTile,
				TileID:       tileID,
				FrameSeq:     frameSeq,
				PacketID:     packetID,
				TotalPackets: totalTileFragments,
				Timestamp:    0,
			}
			header.Marshal(packet[:stream.CSPHeaderSize])
			copy(packet[stream.CSPHeaderSize:], payload)

			_, err := conn.WriteToUDP(packet, destAddr)
			if err != nil {
				log.Printf("[rgba-server] tile write error frame=%d tile=%d dest=%s err=%v",
					frameSeq, tileID, destAddr.String(), err)
				return err
			}
			sentCount++

			packetID++
		}
	}

	return nil
}

// Close closes the pipeline
func (p *ServerPipeline) Close() error {
	return nil
}
