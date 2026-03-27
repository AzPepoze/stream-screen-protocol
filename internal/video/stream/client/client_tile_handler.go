package client

import (
	"fmt"
	"log"
	"sort"
	"streamscreen/internal/video/stream"
	"time"
)

// handleTilePacket processes incoming tile packets, handling fragmentation
func (r *ClientReceiver) handleTilePacket(h stream.PacketHeader, buf []byte) {
	if r.tileGrid == nil {
		return
	}

	// Extract payload (skip header)
	payload := buf[stream.CSPHeaderSize:]
	if len(payload) < 2 {
		log.Printf("[client] Tile packet too small: %d bytes", len(payload))
		return
	}

	// Single-packet tile - process immediately
	if h.TotalPackets <= 1 {
		_, tileID, pixels, err := stream.UnmarshalTile(payload)
		if err == nil {
			r.frameBufferMu.Lock()
			r.tileGrid.SetTile(tileID, pixels, r.frameBuffer)
			r.frameBufferMu.Unlock()
			r.frameDirty.Store(true)
		} else {
			log.Printf("[client] Failed to unmarshal tile: %v", err)
		}
		return
	}

	// Fragmented tile - accumulate all fragments
	key := fmt.Sprintf("%d:%d", h.FrameSeq, h.TileID)

	r.tileFragBufMu.Lock()

	fb, ok := r.tileFragBuf[key]
	if !ok {
		fb = &TileFragmentBuffer{
			fragments:    make(map[uint32][]byte),
			totalPackets: h.TotalPackets,
			receivedAt:   time.Now(),
			tileID:       h.TileID,
		}
		r.tileFragBuf[key] = fb
	}

	// Store fragment payload
	fragment := make([]byte, len(payload))
	copy(fragment, payload)
	fb.fragments[h.PacketID] = fragment

	// Check if all fragments received
	if uint32(len(fb.fragments)) == fb.totalPackets {
		reassembled := r.reassembleTile(fb)
		delete(r.tileFragBuf, key)
		r.tileFragBufMu.Unlock()

		// Unmarshal and draw the reassembled tile
		if reassembled != nil {
			_, tileID, pixels, err := stream.UnmarshalTile(reassembled)
			if err == nil {
				r.frameBufferMu.Lock()
				r.tileGrid.SetTile(tileID, pixels, r.frameBuffer)
				r.frameBufferMu.Unlock()
				r.frameDirty.Store(true)
			} else {
				log.Printf("[client] Failed to unmarshal reassembled tile: %v", err)
			}
		}
	} else {
		r.tileFragBufMu.Unlock()
	}
}

// reassembleTile reassembles fragmented tile packet fragments in order
func (r *ClientReceiver) reassembleTile(fb *TileFragmentBuffer) []byte {
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

// tileRequestLoop monitors tile freshness and requests stale tiles from server
func (r *ClientReceiver) tileRequestLoop() {
	if r.tileGrid == nil {
		log.Printf("Client: tileRequestLoop() starting before TileGrid initialized, returning")
		return
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	initialCheckDone := false

	log.Printf("Client: tileRequestLoop() started - will monitor tile freshness every 3s")

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			tileCount := r.tileGrid.GetTileCount()
			recvCount := r.tileGrid.CountRecentTiles(300 * time.Second)

			// Check if 90% or more of tiles are missing/stale
			staleFraction := 1.0 - (float64(recvCount) / float64(tileCount))

			if staleFraction >= 0.9 {
				// Request full refresh when 90%+ tiles are missing
				log.Printf("Client: %d/%d tiles stale (%.1f%%) - requesting full refresh", tileCount-recvCount, tileCount, staleFraction*100)
				allTiles := make([]uint16, tileCount)
				for i := 0; i < tileCount; i++ {
					allTiles[i] = uint16(i)
				}
				r.tileGrid.MarkRequested(allTiles)
				reqPacket := stream.MarshalTileRequest(allTiles)
				if _, err := r.conn.WriteToUDP(reqPacket, r.serverAddr); err != nil {
					log.Printf("Client: failed to send full tile request: %v", err)
				}
				initialCheckDone = true
			} else if !initialCheckDone && recvCount == tileCount {
				// All tiles received - switch to targeted recovery
				log.Printf("Client: initial full frame received, switching to targeted tile recovery")
				initialCheckDone = true
			} else if initialCheckDone && staleFraction < 0.9 {
				// Normal operation: request tiles stale for >5 seconds
				staleTiles := r.tileGrid.GetStaleTiles(5*time.Second, 1*time.Second)
				if len(staleTiles) > 0 {
					// Limit to 20 tiles to avoid flooding
					if len(staleTiles) > 20 {
						staleTiles = staleTiles[:20]
					}

					log.Printf("Client: requesting %d stale tiles (age > 5s)", len(staleTiles))

					r.tileGrid.MarkRequested(staleTiles)
					reqPacket := stream.MarshalTileRequest(staleTiles)
					if _, err := r.conn.WriteToUDP(reqPacket, r.serverAddr); err != nil {
						log.Printf("Client: failed to send tile request: %v", err)
					}
				}
			}
		}
	}
}
