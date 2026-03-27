package stream

import (
	"encoding/binary"
	"fmt"
)

// CSP constants
const (
	CSPVersion             = 1
	CSPHeaderSize          = 20
	CSPMaxPacketSize       = 1450 // Safe MTU
	CSPMaxPayloadSize      = CSPMaxPacketSize - CSPHeaderSize
	CSPPacketTypeData      = 1
	CSPPacketTypeNACK      = 2
	CSPPacketTypeControl   = 3
	CSPPacketTypeJoin      = 4
	CSPPacketTypeVideoInfo = 5 // Server sends video resolution/fps/gridSize to client
	CSPPacketTypeTile      = 6 // Server sends individual tile data
	CSPPacketTypeTileReq   = 7 // Client requests missing tiles
	CSPPacketTypeAudioInfo = 8 // Server sends audio format metadata to client
	CSPPacketTypeAudioData = 9 // Server sends encoded audio payload
)

// PacketHeader represents the CSP packet header.
type PacketHeader struct {
	Version      uint8
	PacketType   uint8
	TileID       uint16 // For tile packets, empty for others
	FrameSeq     uint32
	PacketID     uint32
	TotalPackets uint32
	Timestamp    uint32
}

// Marshal serializes the header into a byte slice.
func (h *PacketHeader) Marshal(buf []byte) {
	buf[0] = h.Version
	buf[1] = h.PacketType
	binary.BigEndian.PutUint16(buf[2:4], h.TileID) // TileID in padding space
	binary.BigEndian.PutUint32(buf[4:8], h.FrameSeq)
	binary.BigEndian.PutUint32(buf[8:12], h.PacketID)
	binary.BigEndian.PutUint32(buf[12:16], h.TotalPackets)
	binary.BigEndian.PutUint32(buf[16:20], h.Timestamp)
}

// Unmarshal deserializes the header from a byte slice.
func (h *PacketHeader) Unmarshal(buf []byte) error {
	if len(buf) < CSPHeaderSize {
		return fmt.Errorf("buffer too small for CSP header: %d", len(buf))
	}
	h.Version = buf[0]
	h.PacketType = buf[1]
	h.TileID = binary.BigEndian.Uint16(buf[2:4]) // Read TileID from fixed header position
	h.FrameSeq = binary.BigEndian.Uint32(buf[4:8])
	h.PacketID = binary.BigEndian.Uint32(buf[8:12])
	h.TotalPackets = binary.BigEndian.Uint32(buf[12:16])
	h.Timestamp = binary.BigEndian.Uint32(buf[16:20])
	return nil
}

// NACKPayload represents a request for missing packets.
type NACKPayload struct {
	FrameSeq  uint32
	PacketIDs []uint32
}

// ControlFeedback is lightweight congestion feedback sent from client to server.
// queue values are percentages in [0,100].
type ControlFeedback struct {
	FrameQueuePercent uint8
	AudioQueuePercent uint8
	FrameDrops        uint32
	AudioDrops        uint32
	NACKSent          uint32
}

// MarshalNACK serializes a NACK request.
func MarshalNACK(frameSeq uint32, packetIDs []uint32) []byte {
	buf := make([]byte, CSPHeaderSize+4+len(packetIDs)*4)
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeNACK,
		FrameSeq:   frameSeq,
	}
	h.Marshal(buf)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize:CSPHeaderSize+4], uint32(len(packetIDs)))
	for i, id := range packetIDs {
		binary.BigEndian.PutUint32(buf[CSPHeaderSize+4+i*4:CSPHeaderSize+4+(i+1)*4], id)
	}
	return buf
}

// UnmarshalNACK deserializes a NACK request.
func UnmarshalNACK(buf []byte) (uint32, []uint32, error) {
	if len(buf) < CSPHeaderSize+4 {
		return 0, nil, fmt.Errorf("buffer too small for NACK: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf); err != nil {
		return 0, nil, err
	}
	count := binary.BigEndian.Uint32(buf[CSPHeaderSize : CSPHeaderSize+4])
	if len(buf) < CSPHeaderSize+4+int(count)*4 {
		return 0, nil, fmt.Errorf("NACK buffer truncated")
	}
	ids := make([]uint32, count)
	for i := 0; i < int(count); i++ {
		ids[i] = binary.BigEndian.Uint32(buf[CSPHeaderSize+4+i*4 : CSPHeaderSize+4+(i+1)*4])
	}
	return h.FrameSeq, ids, nil
}

// MarshalControlFeedback serializes client congestion feedback.
func MarshalControlFeedback(f ControlFeedback) []byte {
	buf := make([]byte, CSPHeaderSize+16)
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeControl,
	}
	h.Marshal(buf[:CSPHeaderSize])
	buf[CSPHeaderSize] = 1 // payload version
	buf[CSPHeaderSize+1] = f.FrameQueuePercent
	buf[CSPHeaderSize+2] = f.AudioQueuePercent
	// buf[CSPHeaderSize+3] reserved
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+4:CSPHeaderSize+8], f.FrameDrops)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+8:CSPHeaderSize+12], f.AudioDrops)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+12:CSPHeaderSize+16], f.NACKSent)
	return buf
}

// UnmarshalControlFeedback deserializes client congestion feedback.
func UnmarshalControlFeedback(buf []byte) (ControlFeedback, error) {
	if len(buf) < CSPHeaderSize+16 {
		return ControlFeedback{}, fmt.Errorf("buffer too small for control feedback: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return ControlFeedback{}, err
	}
	if h.PacketType != CSPPacketTypeControl {
		return ControlFeedback{}, fmt.Errorf("not a control packet")
	}
	if buf[CSPHeaderSize] != 1 {
		return ControlFeedback{}, fmt.Errorf("unsupported control payload version: %d", buf[CSPHeaderSize])
	}
	return ControlFeedback{
		FrameQueuePercent: buf[CSPHeaderSize+1],
		AudioQueuePercent: buf[CSPHeaderSize+2],
		FrameDrops:        binary.BigEndian.Uint32(buf[CSPHeaderSize+4 : CSPHeaderSize+8]),
		AudioDrops:        binary.BigEndian.Uint32(buf[CSPHeaderSize+8 : CSPHeaderSize+12]),
		NACKSent:          binary.BigEndian.Uint32(buf[CSPHeaderSize+12 : CSPHeaderSize+16]),
	}, nil
}

// MarshalJoin serializes a JOIN packet with an optional endpoint payload.
func MarshalJoin(endpoint string) []byte {
	payload := []byte(endpoint)
	buf := make([]byte, CSPHeaderSize+len(payload))
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeJoin,
	}
	h.Marshal(buf)
	copy(buf[CSPHeaderSize:], payload)
	return buf
}

// UnmarshalJoin extracts the endpoint string from a JOIN packet payload.
func UnmarshalJoin(buf []byte) (string, error) {
	if len(buf) < CSPHeaderSize {
		return "", fmt.Errorf("buffer too small for JOIN: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return "", err
	}
	if h.PacketType != CSPPacketTypeJoin {
		return "", fmt.Errorf("not a JOIN packet")
	}
	if len(buf) == CSPHeaderSize {
		return "", nil
	}
	return string(buf[CSPHeaderSize:]), nil
}

// MarshalVideoInfo serializes video resolution, FPS, grid size, and codec info for the client
func MarshalVideoInfo(width, height, fps, gridSize uint32, codecName string) []byte {
	buf := make([]byte, CSPHeaderSize+16+1+len(codecName))
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeVideoInfo,
	}
	h.Marshal(buf[:CSPHeaderSize])
	binary.BigEndian.PutUint32(buf[CSPHeaderSize:CSPHeaderSize+4], width)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+4:CSPHeaderSize+8], height)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+8:CSPHeaderSize+12], fps)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+12:CSPHeaderSize+16], gridSize)
	buf[CSPHeaderSize+16] = uint8(len(codecName))
	copy(buf[CSPHeaderSize+17:], []byte(codecName))
	return buf
}

// UnmarshalVideoInfo extracts video info including grid size and codec name from packet
func UnmarshalVideoInfo(buf []byte) (width, height, fps, gridSize uint32, codecName string, err error) {
	if len(buf) < CSPHeaderSize+17 {
		return 0, 0, 0, 0, "", fmt.Errorf("buffer too small for VideoInfo: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return 0, 0, 0, 0, "", err
	}
	if h.PacketType != CSPPacketTypeVideoInfo {
		return 0, 0, 0, 0, "", fmt.Errorf("not a VideoInfo packet")
	}
	width = binary.BigEndian.Uint32(buf[CSPHeaderSize : CSPHeaderSize+4])
	height = binary.BigEndian.Uint32(buf[CSPHeaderSize+4 : CSPHeaderSize+8])
	fps = binary.BigEndian.Uint32(buf[CSPHeaderSize+8 : CSPHeaderSize+12])
	gridSize = binary.BigEndian.Uint32(buf[CSPHeaderSize+12 : CSPHeaderSize+16])
	codecNameLen := int(buf[CSPHeaderSize+16])
	if len(buf) < CSPHeaderSize+17+codecNameLen {
		return 0, 0, 0, 0, "", fmt.Errorf("VideoInfo buffer truncated for codec name")
	}
	codecName = string(buf[CSPHeaderSize+17 : CSPHeaderSize+17+codecNameLen])
	return width, height, fps, gridSize, codecName, nil
}

// MarshalTile serializes a screen tile (fragment of screen in grid)
// tileID: position in linear grid (0 = top-left for 10x10)
// pixels: RGBA pixel data for this tile
func MarshalTile(frameSeq uint32, tileID uint16, pixels []byte) []byte {
	buf := make([]byte, CSPHeaderSize+2+len(pixels))
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeTile,
		FrameSeq:   frameSeq,
	}
	h.Marshal(buf[:CSPHeaderSize])
	binary.BigEndian.PutUint16(buf[CSPHeaderSize:CSPHeaderSize+2], tileID)
	copy(buf[CSPHeaderSize+2:], pixels)
	return buf
}

// UnmarshalTile extracts tile data from packet
func UnmarshalTile(buf []byte) (frameSeq uint32, tileID uint16, pixels []byte, err error) {
	if len(buf) < CSPHeaderSize+2 {
		return 0, 0, nil, fmt.Errorf("buffer too small for Tile: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return 0, 0, nil, err
	}
	if h.PacketType != CSPPacketTypeTile {
		return 0, 0, nil, fmt.Errorf("not a Tile packet")
	}
	tileID = binary.BigEndian.Uint16(buf[CSPHeaderSize : CSPHeaderSize+2])
	pixels = buf[CSPHeaderSize+2:]
	return h.FrameSeq, tileID, pixels, nil
}

// MarshalTileRequest serializes a request for missing tiles
func MarshalTileRequest(tileIDs []uint16) []byte {
	buf := make([]byte, CSPHeaderSize+1+len(tileIDs)*2)
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeTileReq,
	}
	h.Marshal(buf[:CSPHeaderSize])
	buf[CSPHeaderSize] = uint8(len(tileIDs))
	for i, id := range tileIDs {
		binary.BigEndian.PutUint16(buf[CSPHeaderSize+1+i*2:CSPHeaderSize+1+(i+1)*2], id)
	}
	return buf
}

// UnmarshalTileRequest extracts tile request IDs from packet
func UnmarshalTileRequest(buf []byte) ([]uint16, error) {
	if len(buf) < CSPHeaderSize+1 {
		return nil, fmt.Errorf("buffer too small for TileRequest: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return nil, err
	}
	if h.PacketType != CSPPacketTypeTileReq {
		return nil, fmt.Errorf("not a TileRequest packet")
	}
	count := int(buf[CSPHeaderSize])
	if len(buf) < CSPHeaderSize+1+count*2 {
		return nil, fmt.Errorf("TileRequest buffer truncated")
	}
	tileIDs := make([]uint16, count)
	for i := 0; i < count; i++ {
		tileIDs[i] = binary.BigEndian.Uint16(buf[CSPHeaderSize+1+i*2 : CSPHeaderSize+1+(i+1)*2])
	}
	return tileIDs, nil
}

// MarshalAudioInfo serializes audio stream metadata.
func MarshalAudioInfo(sampleRate, channels, frameMS, bitrateKbps uint32, codecName string) []byte {
	buf := make([]byte, CSPHeaderSize+16+1+len(codecName))
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeAudioInfo,
	}
	h.Marshal(buf[:CSPHeaderSize])
	binary.BigEndian.PutUint32(buf[CSPHeaderSize:CSPHeaderSize+4], sampleRate)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+4:CSPHeaderSize+8], channels)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+8:CSPHeaderSize+12], frameMS)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+12:CSPHeaderSize+16], bitrateKbps)
	buf[CSPHeaderSize+16] = uint8(len(codecName))
	copy(buf[CSPHeaderSize+17:], []byte(codecName))
	return buf
}

// UnmarshalAudioInfo extracts audio stream metadata.
func UnmarshalAudioInfo(buf []byte) (sampleRate, channels, frameMS, bitrateKbps uint32, codecName string, err error) {
	if len(buf) < CSPHeaderSize+17 {
		return 0, 0, 0, 0, "", fmt.Errorf("buffer too small for AudioInfo: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return 0, 0, 0, 0, "", err
	}
	if h.PacketType != CSPPacketTypeAudioInfo {
		return 0, 0, 0, 0, "", fmt.Errorf("not an AudioInfo packet")
	}
	sampleRate = binary.BigEndian.Uint32(buf[CSPHeaderSize : CSPHeaderSize+4])
	channels = binary.BigEndian.Uint32(buf[CSPHeaderSize+4 : CSPHeaderSize+8])
	frameMS = binary.BigEndian.Uint32(buf[CSPHeaderSize+8 : CSPHeaderSize+12])
	bitrateKbps = binary.BigEndian.Uint32(buf[CSPHeaderSize+12 : CSPHeaderSize+16])
	codecNameLen := int(buf[CSPHeaderSize+16])
	if len(buf) < CSPHeaderSize+17+codecNameLen {
		return 0, 0, 0, 0, "", fmt.Errorf("AudioInfo buffer truncated for codec name")
	}
	codecName = string(buf[CSPHeaderSize+17 : CSPHeaderSize+17+codecNameLen])
	return sampleRate, channels, frameMS, bitrateKbps, codecName, nil
}
