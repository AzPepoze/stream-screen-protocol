package server

import (
	"hash/crc32"
	"log"
	"sync"
	"time"

	"streamscreen/internal/video/stream"
)

// TileBuffer manages screen tiles for delta encoding
type TileBuffer struct {
	gridSize       int // N for NxN grid (e.g., 10 = 10x10)
	tileCount      int // Total tiles = gridSize * gridSize
	frameWidth     int
	frameHeight    int
	tileWidth      int
	tileHeight     int
	tiles          [][]byte // Current frame tiles
	prevTiles      [][]byte // Previous frame tiles
	tileHashes     []uint32 // CRC32 hash of current tiles
	prevHashes     []uint32 // CRC32 hash of previous tiles
	lastUpdateAt   time.Time
	requestedTiles map[uint16]bool // Tiles client requested
	requestedMu    sync.Mutex
	mu             sync.RWMutex
}

// NewTileBuffer creates a new tile buffer
func NewTileBuffer(gridSize, width, height int) *TileBuffer {
	tileCount := gridSize * gridSize
	tileWidth := width / gridSize
	tileHeight := height / gridSize

	return &TileBuffer{
		gridSize:       gridSize,
		tileCount:      tileCount,
		frameWidth:     width,
		frameHeight:    height,
		tileWidth:      tileWidth,
		tileHeight:     tileHeight,
		tiles:          make([][]byte, tileCount),
		prevTiles:      make([][]byte, tileCount),
		tileHashes:     make([]uint32, tileCount),
		prevHashes:     make([]uint32, tileCount),
		lastUpdateAt:   time.Now(),
		requestedTiles: make(map[uint16]bool),
	}
}

// UpdateTiles extracts and hashes tiles from RGBA frame data
// Returns list of changed tile IDs
func (tb *TileBuffer) UpdateTiles(rgbaData []byte) []uint16 {
	if len(rgbaData) != tb.frameWidth*tb.frameHeight*4 {
		log.Printf("TileBuffer: wrong frame size: got %d, expected %d", len(rgbaData), tb.frameWidth*tb.frameHeight*4)
		return nil
	}

	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Swap current -> previous
	tb.prevTiles, tb.tiles = tb.tiles, tb.prevTiles
	tb.prevHashes, tb.tileHashes = tb.tileHashes, tb.prevHashes

	var changed []uint16

	// Extract each tile and compute hash
	for y := 0; y < tb.gridSize; y++ {
		for x := 0; x < tb.gridSize; x++ {
			tileID := y*tb.gridSize + x
			startX := x * tb.tileWidth
			startY := y * tb.tileHeight

			// Clamp to frame boundaries
			endX := startX + tb.tileWidth
			if endX > tb.frameWidth {
				endX = tb.frameWidth
			}
			endY := startY + tb.tileHeight
			if endY > tb.frameHeight {
				endY = tb.frameHeight
			}

			actualWidth := endX - startX
			actualHeight := endY - startY
			pixelsPerTile := actualWidth * actualHeight * 4

			if tb.tiles[tileID] == nil || cap(tb.tiles[tileID]) < pixelsPerTile {
				tb.tiles[tileID] = make([]byte, pixelsPerTile)
			} else {
				tb.tiles[tileID] = tb.tiles[tileID][:pixelsPerTile]
			}

			// Write tile pixels directly into reusable tile buffer.
			idx := 0
			for py := startY; py < endY; py++ {
				srcStart := (py*tb.frameWidth + startX) * 4
				srcEnd := srcStart + actualWidth*4
				copy(tb.tiles[tileID][idx:], rgbaData[srcStart:srcEnd])
				idx += actualWidth * 4
			}

			hash := crc32.ChecksumIEEE(tb.tiles[tileID])
			tb.tileHashes[tileID] = hash

			// Check if changed
			if hash != tb.prevHashes[tileID] {
				changed = append(changed, uint16(tileID))
			}
		}
	}

	tb.lastUpdateAt = time.Now()
	return changed
}

// SetRequestedTiles marks tiles that client requested
func (tb *TileBuffer) SetRequestedTiles(tileIDs []uint16) {
	tb.requestedMu.Lock()
	defer tb.requestedMu.Unlock()

	tb.requestedTiles = make(map[uint16]bool)
	for _, id := range tileIDs {
		tb.requestedTiles[id] = true
	}
}

// GetTilesToSend returns tiles to send, prioritizing requested tiles
func (tb *TileBuffer) GetTilesToSend(changed []uint16) []uint16 {
	tb.requestedMu.Lock()
	defer tb.requestedMu.Unlock()

	// Priority: requested tiles first, then changed tiles
	result := make([]uint16, 0, tb.tileCount)
	sent := make(map[uint16]bool)

	// Add requested tiles first
	for tileID := range tb.requestedTiles {
		result = append(result, tileID)
		sent[tileID] = true
	}

	// Then add changed tiles (skip already sent)
	for _, tileID := range changed {
		if !sent[tileID] {
			result = append(result, tileID)
		}
	}

	// Clear requested tiles after sending
	tb.requestedTiles = make(map[uint16]bool)

	return result
}

// GetTile returns pixel data for specific tile
func (tb *TileBuffer) GetTile(tileID uint16) []byte {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	if tileID >= uint16(tb.tileCount) {
		return nil
	}
	return tb.tiles[tileID]
}

// extractTile extracts RGBA pixel data for a tile
func (tb *TileBuffer) extractTile(rgbaData []byte, tileX, tileY int) []byte {
	startX := tileX * tb.tileWidth
	startY := tileY * tb.tileHeight

	// Clamp to frame boundaries
	endX := startX + tb.tileWidth
	if endX > tb.frameWidth {
		endX = tb.frameWidth
	}
	endY := startY + tb.tileHeight
	if endY > tb.frameHeight {
		endY = tb.frameHeight
	}

	actualWidth := endX - startX
	actualHeight := endY - startY
	pixelsPerTile := actualWidth * actualHeight * 4

	tileData := make([]byte, pixelsPerTile)
	idx := 0

	for y := startY; y < endY; y++ {
		srcStart := (y*tb.frameWidth + startX) * 4
		srcEnd := srcStart + actualWidth*4
		copy(tileData[idx:], rgbaData[srcStart:srcEnd])
		idx += actualWidth * 4
	}

	return tileData
}

// EncodeTile creates a Tile packet for the given tile ID
func (tb *TileBuffer) EncodeTile(frameSeq uint32, tileID uint16) []byte {
	tileData := tb.GetTile(tileID)
	if tileData == nil {
		return nil
	}
	return stream.MarshalTile(frameSeq, tileID, tileData)
}
