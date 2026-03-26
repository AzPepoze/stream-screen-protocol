package tiles

// GridLayout contains shared tile grid calculations used by both server and client
type GridLayout struct {
	GridSize    int // N for NxN grid (e.g., 10 = 10x10)
	TileCount   int // Total tiles = GridSize * GridSize
	FrameWidth  int
	FrameHeight int
	TileWidth   int
	TileHeight  int
}

// NewGridLayout creates a new grid layout for tiles
func NewGridLayout(gridSize, frameWidth, frameHeight int) *GridLayout {
	tileCount := gridSize * gridSize
	tileWidth := frameWidth / gridSize
	tileHeight := frameHeight / gridSize

	return &GridLayout{
		GridSize:    gridSize,
		TileCount:   tileCount,
		FrameWidth:  frameWidth,
		FrameHeight: frameHeight,
		TileWidth:   tileWidth,
		TileHeight:  tileHeight,
	}
}

// TileInfo returns the pixel coordinates and size of a tile
func (g *GridLayout) TileInfo(tileID int) (startX, startY, endX, endY int) {
	if tileID < 0 || tileID >= g.TileCount {
		return 0, 0, 0, 0
	}

	tileY := tileID / g.GridSize
	tileX := tileID % g.GridSize

	startX = tileX * g.TileWidth
	startY = tileY * g.TileHeight
	endX = startX + g.TileWidth
	endY = startY + g.TileHeight

	// Clamp to frame boundaries
	if endX > g.FrameWidth {
		endX = g.FrameWidth
	}
	if endY > g.FrameHeight {
		endY = g.FrameHeight
	}

	return
}

// TileIDFromGrid converts grid coordinates (tileX, tileY) to tile ID
func (g *GridLayout) TileIDFromGrid(tileX, tileY int) int {
	if tileX < 0 || tileX >= g.GridSize || tileY < 0 || tileY >= g.GridSize {
		return -1
	}
	return tileY*g.GridSize + tileX
}

// GridCoordFromID converts tile ID to grid coordinates
func (g *GridLayout) GridCoordFromID(tileID int) (tileX, tileY int) {
	if tileID < 0 || tileID >= g.TileCount {
		return -1, -1
	}
	return tileID % g.GridSize, tileID / g.GridSize
}

// ExtractTileFromRGBA extracts tile data from RGBA frame buffer by tile ID
func (g *GridLayout) ExtractTileFromRGBA(rgbaData []byte, tileID int) []byte {
	startX, startY, endX, endY := g.TileInfo(tileID)
	tileWidth := endX - startX
	tileHeight := endY - startY

	tileData := make([]byte, tileWidth*tileHeight*4)
	for y := startY; y < endY; y++ {
		srcStart := (y*g.FrameWidth + startX) * 4
		dstStart := (y - startY) * tileWidth * 4
		copy(tileData[dstStart:dstStart+tileWidth*4], rgbaData[srcStart:srcStart+tileWidth*4])
	}
	return tileData
}

// CopyTileToFrame copies tile data into a frame buffer at the correct tile position
func (g *GridLayout) CopyTileToFrame(frame []byte, tileID int, tileData []byte) {
	startX, startY, endX, endY := g.TileInfo(tileID)
	tileWidth := endX - startX

	for y := startY; y < endY; y++ {
		dstStart := (y*g.FrameWidth + startX) * 4
		srcStart := (y - startY) * tileWidth * 4
		actualWidth := endX - startX
		if srcStart+actualWidth*4 <= len(tileData) {
			copy(frame[dstStart:dstStart+actualWidth*4], tileData[srcStart:srcStart+actualWidth*4])
		}
	}
}

// FillTileWithBlack fills a tile region in frame with black pixels
func (g *GridLayout) FillTileWithBlack(frame []byte, tileID int) {
	startX, startY, endX, endY := g.TileInfo(tileID)
	tileWidth := endX - startX

	// Fill with black (RGBA = 0,0,0,255)
	for y := startY; y < endY; y++ {
		dstStart := (y*g.FrameWidth + startX) * 4
		for i := 0; i < tileWidth; i++ {
			frame[dstStart+i*4+3] = 255 // Alpha
		}
	}
}
