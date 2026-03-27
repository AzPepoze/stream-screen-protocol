package client

import (
	"log"
	"sync"
	"time"
)

// TileGrid manages received screen tiles for reconstruction
type TileGrid struct {
	gridSize     int
	tileCount    int
	frameWidth   int
	frameHeight  int
	tileWidth    int
	tileHeight   int
	tiles        [][]byte    // Current tiles
	tileRecvTime []time.Time // Last received time for each tile
	requestedAt  []time.Time // Last time we requested this tile
	mu           sync.RWMutex
}

// NewTileGrid creates a new tile grid
func NewTileGrid(gridSize, width, height int) *TileGrid {
	tileCount := gridSize * gridSize
	tileWidth := width / gridSize
	tileHeight := height / gridSize

	tg := &TileGrid{
		gridSize:     gridSize,
		tileCount:    tileCount,
		frameWidth:   width,
		frameHeight:  height,
		tileWidth:    tileWidth,
		tileHeight:   tileHeight,
		tiles:        make([][]byte, tileCount),
		tileRecvTime: make([]time.Time, tileCount),
		requestedAt:  make([]time.Time, tileCount),
	}

	// Initialize all tiles with zero time (never received)
	return tg
}

// SetTile writes tile pixels directly to the frame buffer at the correct tile position
func (tg *TileGrid) SetTile(tileID uint16, pixelData []byte, frameBuffer []byte) {
	tg.mu.Lock()
	defer tg.mu.Unlock()

	if tileID >= uint16(tg.tileCount) {
		log.Printf("[TILE] ID %d out of range (max %d)", tileID, tg.tileCount)
		return
	}

	// Calculate tile grid position
	tileY := int(tileID) / tg.gridSize
	tileX := int(tileID) % tg.gridSize

	// Calculate pixel coordinates of this tile
	startX := tileX * tg.tileWidth
	startY := tileY * tg.tileHeight
	endX := startX + tg.tileWidth
	endY := startY + tg.tileHeight

	// Clamp to frame boundaries
	if endX > tg.frameWidth {
		endX = tg.frameWidth
	}
	if endY > tg.frameHeight {
		endY = tg.frameHeight
	}

	actualWidth := endX - startX

	// Write tile pixels to frame buffer at correct position
	pixelIdx := 0
	for y := startY; y < endY; y++ {
		dstStart := (y*tg.frameWidth + startX) * 4
		copySize := actualWidth * 4
		if pixelIdx+copySize <= len(pixelData) && dstStart+copySize <= len(frameBuffer) {
			copy(frameBuffer[dstStart:dstStart+copySize], pixelData[pixelIdx:pixelIdx+copySize])
			pixelIdx += copySize
		} else {
			log.Printf("[TILE] ERROR: bounds check failed - pixelIdx=%d pixelLen=%d OR dstStart=%d frameLen=%d",
				pixelIdx+copySize, len(pixelData), dstStart+copySize, len(frameBuffer))
			return
		}
	}

	tg.tileRecvTime[tileID] = time.Now()
}

// GetStaleTiles returns tile IDs that haven't been updated for more than timeout
// Excludes tiles that were recently requested (debounce)
func (tg *TileGrid) GetStaleTiles(timeout time.Duration, requestDebounce time.Duration) []uint16 {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	now := time.Now()
	var staleTiles []uint16

	for i := 0; i < tg.tileCount; i++ {
		lastRecv := tg.tileRecvTime[i]
		lastReq := tg.requestedAt[i]

		// Check if tile has never been received or is older than timeout
		timeSinceRecv := now.Sub(lastRecv)
		if lastRecv.IsZero() || timeSinceRecv > timeout {
			// Check if we recently requested this tile (debounce)
			timeSinceReq := now.Sub(lastReq)
			if lastReq.IsZero() || timeSinceReq > requestDebounce {
				staleTiles = append(staleTiles, uint16(i))
			}
		}
	}

	return staleTiles
}

// MarkRequested updates the request timestamp for tiles
func (tg *TileGrid) MarkRequested(tileIDs []uint16) {
	tg.mu.Lock()
	defer tg.mu.Unlock()

	now := time.Now()
	for _, id := range tileIDs {
		if id < uint16(tg.tileCount) {
			tg.requestedAt[id] = now
		}
	}
}

// CountRecentTiles returns how many tiles have been received in the last duration
func (tg *TileGrid) CountRecentTiles(duration time.Duration) int {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	now := time.Now()
	threshold := now.Add(-duration)
	count := 0

	for _, t := range tg.tileRecvTime {
		if !t.IsZero() && t.After(threshold) {
			count++
		}
	}

	return count
}

// GetTileCount returns the total number of tiles in the grid
func (tg *TileGrid) GetTileCount() int {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	return tg.tileCount
}
