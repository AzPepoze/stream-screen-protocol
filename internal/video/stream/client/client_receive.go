package client

import (
	"fmt"
	"log"
	"sort"
	"streamscreen/internal/video/stream"
	"time"
)

func (r *ClientReceiver) receiveLoop() {
	buf := make([]byte, 65535)
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
			n, _, err := r.conn.ReadFromUDP(buf)
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

			// log.Printf("[client] received packet type=%d frame=%d pkt=%d/%d total bytes=%d", h.PacketType, h.FrameSeq, h.PacketID, h.TotalPackets, n)

			// Handle video info from server
			if h.PacketType == stream.CSPPacketTypeVideoInfo {
				w, ht, fps, gridSize, codecName, err := stream.UnmarshalVideoInfo(buf[:n])
				if err == nil {
					log.Printf("Client: VideoInfo - %dx%d @ %d fps, gridSize=%d, codec=%s", w, ht, fps, gridSize, codecName)
					r.videoInfoMu.Lock()
					r.videoWidth = w
					r.videoHeight = ht
					r.videoFPS = fps
					r.tileGridSize = int(gridSize)
					r.codecName = codecName
					r.videoInfoMu.Unlock()
				}
				continue
			}

			if h.PacketType == stream.CSPPacketTypeTile {
				if r.rgbaPipeline != nil && r.tileGrid != nil {
					r.frameBufferMu.Lock()
					r.rgbaPipeline.HandleTilePacket(h, buf[:n], func(tileID uint16, pixels []byte) {
						r.tileGrid.SetTile(tileID, pixels, r.frameBuffer)
						r.frameDirty.Store(true)
					})
					r.frameBufferMu.Unlock()
				}
				continue
			}

			if h.PacketType != stream.CSPPacketTypeData {
				continue
			}

			payload := buf[stream.CSPHeaderSize:n]
			if data, seq := r.jitterBuffer.Push(h, payload); data != nil {
				r.enqueueFrameLatest(assembledFrame{Seq: seq, Data: data})
			}
		}
	}
}

func (r *ClientReceiver) enqueueFrameLatest(f assembledFrame) {
	select {
	case <-r.ctx.Done():
		return
	case r.frameChan <- f:
		return
	default:
	}

	// Queue full: drop one stale frame then push the latest frame.
	select {
	case <-r.frameChan:
	default:
	}

	select {
	case <-r.ctx.Done():
	case r.frameChan <- f:
	default:
		log.Printf("frame channel full, dropping frame=%d", f.Seq)
	}
}

func (r *ClientReceiver) joinLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Simplified: don't attempt STUN or outbound discovery. Send an
	// empty JOIN payload so the server can use the observed source
	// address as the client endpoint.
	packet := stream.MarshalJoin("")

	log.Printf("Client: joinLoop() STARTING, will send JOIN to %s every 1s", r.serverAddr.String())
	for {
		// Clear any stale write deadline before sending the JOIN so we don't
		// get an immediate i/o timeout from a previously set deadline.
		_ = r.conn.SetWriteDeadline(time.Time{})
		// Send initial join with advertised endpoint
		if _, err := r.conn.WriteToUDP(packet, r.serverAddr); err != nil {
			log.Printf("Client: joinLoop() JOIN write error: %v", err)
		}

		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *ClientReceiver) nackLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case req := <-r.jitterBuffer.nackChan:
			packet := stream.MarshalNACK(req.FrameSeq, req.PacketIDs)
			_, _ = r.conn.WriteToUDP(packet, r.serverAddr)
		}
	}
}

func (r *ClientReceiver) appsrcLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case f, ok := <-r.frameChan:
			if !ok {
				return
			}

			if r.currentCodecName() == "h264" {
				if err := r.HandleH264Frame(f.Data); err != nil {
					r.logH264DecodeError(f.Seq, err)
				}
				continue
			}

			if len(f.Data) == 0 {
				continue
			}
			r.pixelsMu.Lock()
			if len(r.pixels) == len(f.Data) {
				copy(r.prevPixels, r.pixels)
				copy(r.pixels, f.Data)
				r.frameSeq++
			}
			r.pixelsMu.Unlock()
		}
	}
}

func (r *ClientReceiver) logH264DecodeError(seq uint32, err error) {
	r.h264ErrMu.Lock()
	defer r.h264ErrMu.Unlock()

	r.h264ErrCount++
	now := time.Now()
	if now.Sub(r.h264ErrLogAt) < time.Second {
		return
	}

	log.Printf("Client: H264 decode errors=%d latest_frame=%d err=%v", r.h264ErrCount, seq, err)
	r.h264ErrLogAt = now
	r.h264ErrCount = 0
}

func (r *ClientReceiver) tileRequestLoop() {
	if r.tileGrid == nil {
		log.Printf("Client: tileRequestLoop() starting before TileGrid initialized, returning")
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	log.Printf("Client: tileRequestLoop() started - will request stale tiles every 5s with 1s debounce")

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			// Find tiles that haven't been received for >5 seconds
			staleTiles := r.tileGrid.GetStaleTiles(5*time.Second, 1*time.Second)
			if len(staleTiles) > 0 {
				// Limit to first 20 tiles to avoid flooding
				if len(staleTiles) > 20 {
					staleTiles = staleTiles[:20]
				}

				log.Printf("Client: requesting %d stale tiles", len(staleTiles))

				// Mark as requested (for debounce)
				r.tileGrid.MarkRequested(staleTiles)

				// Send tile request packet
				reqPacket := stream.MarshalTileRequest(staleTiles)
				if _, err := r.conn.WriteToUDP(reqPacket, r.serverAddr); err != nil {
					log.Printf("Client: failed to send tile request: %v", err)
				}
			}
		}
	}
}

// compositeTilesWithFallback composites tiles from current and previous frames
// Missing tiles (where data wasn't fully received) use pixels from prevPixels
func (r *ClientReceiver) compositeTilesWithFallback(incomplete []byte) []byte {
	r.pixelsMu.RLock()
	defer r.pixelsMu.RUnlock()

	// For now, just use the previous frame as fallback
	// In a full implementation, we'd detect which tiles are corrupted
	// and only use prevPixels for those specific tiles

	// Simple heuristic: if frame has many zeros (indicating data gaps), use prev
	zeroCount := 0
	sampleSize := len(incomplete)
	if sampleSize > 1000 {
		sampleSize = 1000
	}
	for i := 0; i < sampleSize; i++ {
		if incomplete[i] == 0 {
			zeroCount++
		}
	}

	// If more than 20% zeros, likely corrupted - use previous
	if float64(zeroCount) > float64(sampleSize)*0.2 {
		result := make([]byte, len(incomplete))
		copy(result, r.prevPixels)
		return result
	}

	return incomplete
}

// handleTilePacket processes incoming tile packets, handling fragmentation by drawing each fragment
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

	// Check if this is a fragmented tile or complete
	if h.TotalPackets <= 1 {
		// Single-packet tile - process immediately
		_, tileID, pixels, err := stream.UnmarshalTile(payload)
		if err == nil {
			r.frameBufferMu.Lock()
			r.tileGrid.SetTile(tileID, pixels, r.frameBuffer)
			r.frameBufferMu.Unlock()
			log.Printf("[client] Tile %d received (complete, %d bytes)", tileID, len(pixels))
		} else {
			log.Printf("[client] Failed to unmarshal tile: %v", err)
		}
		return
	}

	// Fragmented tile - accumulate all fragments for this tile
	// Use FrameSeq + TileID (from header) as key
	key := fmt.Sprintf("%d:%d", h.FrameSeq, h.TileID)

	r.tileFragBufMu.Lock()

	fb, ok := r.tileFragBuf[key]
	if !ok {
		fb = &TileFragmentBuffer{
			fragments:    make(map[uint32][]byte),
			totalPackets: h.TotalPackets,
			receivedAt:   time.Now(),
			tileID:       h.TileID, // Tile ID is in header!
		}
		r.tileFragBuf[key] = fb
		log.Printf("[client] KEY=%s (TileID=%d): starting fragmented tile reassembly - need %d fragments", key, h.TileID, h.TotalPackets)
	}

	// Store fragment payload
	fragment := make([]byte, len(payload))
	copy(fragment, payload)
	fb.fragments[h.PacketID] = fragment

	// Check if all fragments are received
	if uint32(len(fb.fragments)) == fb.totalPackets {
		log.Printf("[client] KEY=%s (TileID=%d): ALL %d fragments received! Reassembling...", key, fb.tileID, h.TotalPackets)

		// Reassemble the complete tile packet
		reassembled := r.reassembleTile(fb)
		delete(r.tileFragBuf, key)
		r.tileFragBufMu.Unlock()

		// Unmarshal and draw the reassembled tile
		if reassembled != nil {
			_, tileID, pixels, err := stream.UnmarshalTile(reassembled)
			if err == nil {
				r.frameBufferMu.Lock()
				log.Printf("[client] DRAWING Tile %d to frame buffer (%d bytes)", tileID, len(pixels))
				r.tileGrid.SetTile(tileID, pixels, r.frameBuffer)
				r.frameBufferMu.Unlock()
				log.Printf("[client] DONE: Tile %d written to frame buffer", tileID)
			} else {
				log.Printf("[client] FAILED: unmarshal reassembled tile: %v", err)
			}
		} else {
			log.Printf("[client] FAILED: reassemble returned nil")
		}
	} else {
		r.tileFragBufMu.Unlock()
	}
}

// reassembleTile reassembles fragmented tile packet fragments
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
