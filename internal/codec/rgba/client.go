package rgba

import (
	"fmt"
	"log"
	"sort"
	"streamscreen/internal/stream"
	"time"
)

// ClientPipeline handles RGBA tile-based reception and reassembly
type ClientPipeline struct {
	tileFragBuf map[string]*TileFragmentBuffer
	config      map[string]interface{}
}

// TileFragmentBuffer holds reassembly data for fragmented tiles
type TileFragmentBuffer struct {
	fragments    map[uint32][]byte
	totalPackets uint32
	tileID       uint16
	receivedAt   time.Time
}

// NewClientPipeline creates a new RGBA client pipeline
func NewClientPipeline(cfg map[string]interface{}) *ClientPipeline {
	if cfg == nil {
		cfg = make(map[string]interface{})
	}
	return &ClientPipeline{
		tileFragBuf: make(map[string]*TileFragmentBuffer),
		config:      cfg,
	}
}

// HandleTilePacket processes incoming tile packets, handling fragmentation
// setTileFunc should be called to store the reassembled tile
func (p *ClientPipeline) HandleTilePacket(h stream.PacketHeader, buf []byte, setTileFunc func(tileID uint16, pixels []byte)) error {
	if setTileFunc == nil {
		return fmt.Errorf("setTileFunc is required")
	}

	// Extract payload (skip header)
	payload := buf[stream.CSPHeaderSize:]
	if len(payload) < 2 {
		log.Printf("[rgba-client] Tile packet too small: %d bytes", len(payload))
		return fmt.Errorf("tile packet too small")
	}

	// Check if this is a fragmented tile or complete
	if h.TotalPackets <= 1 {
		// Single-packet tile - process immediately
		_, tileID, pixels, err := stream.UnmarshalTile(payload)
		if err == nil {
			setTileFunc(tileID, pixels)
		} else {
			log.Printf("[rgba-client] Failed to unmarshal tile: %v", err)
			return err
		}
		return nil
	}

	// Fragmented tile - accumulate all fragments for this tile
	key := fmt.Sprintf("%d:%d", h.FrameSeq, h.TileID)

	fb, ok := p.tileFragBuf[key]
	if !ok {
		fb = &TileFragmentBuffer{
			fragments:    make(map[uint32][]byte),
			totalPackets: h.TotalPackets,
			receivedAt:   time.Now(),
			tileID:       h.TileID,
		}
		p.tileFragBuf[key] = fb
	}

	// Store fragment payload
	fragment := make([]byte, len(payload))
	copy(fragment, payload)
	fb.fragments[h.PacketID] = fragment

	// Check if all fragments are received
	if uint32(len(fb.fragments)) == fb.totalPackets {
		// Reassemble the complete tile packet
		reassembled := p.reassembleTile(fb)
		delete(p.tileFragBuf, key)

		// Unmarshal and draw the reassembled tile
		if reassembled != nil {
			_, tileID, pixels, err := stream.UnmarshalTile(reassembled)
			if err == nil {
				setTileFunc(tileID, pixels)
				return nil
			} else {
				log.Printf("[rgba-client] FAILED: unmarshal reassembled tile: %v", err)
				return err
			}
		} else {
			log.Printf("[rgba-client] FAILED: reassemble returned nil")
			return fmt.Errorf("reassemble returned nil")
		}
	}

	return nil
}

// reassembleTile reassembles fragmented tile packet fragments
func (p *ClientPipeline) reassembleTile(fb *TileFragmentBuffer) []byte {
	if len(fb.fragments) != int(fb.totalPackets) {
		return nil
	}

	// Sort fragment IDs
	ids := make([]int, 0, len(fb.fragments))
	for id := range fb.fragments {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)

	// Calculate total reassembled size
	totalLen := 0
	for _, id := range ids {
		totalLen += len(fb.fragments[uint32(id)])
	}

	// Reassemble by copying fragments in order
	result := make([]byte, totalLen)
	offset := 0
	for _, id := range ids {
		frag := fb.fragments[uint32(id)]
		copy(result[offset:], frag)
		offset += len(frag)
	}

	return result
}

// Close closes the pipeline
func (p *ClientPipeline) Close() error {
	p.tileFragBuf = make(map[string]*TileFragmentBuffer)
	return nil
}
