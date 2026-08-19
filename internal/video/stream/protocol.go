package stream

import (
	"encoding/binary"
	"fmt"
	"time"
)

// CSP constants
const (
	CSPVersion               = 2
	CSPHeaderSize            = 20
	CSPMaxPacketSize         = 1450 // Safe MTU
	CSPMaxPayloadSize        = CSPMaxPacketSize - CSPHeaderSize
	CSPPacketTypeData        = 1
	CSPPacketTypeNACK        = 2
	CSPPacketTypeControl     = 3
	CSPPacketTypeJoin        = 4
	CSPPacketTypeVideoInfo   = 5  // Server sends video resolution/fps/gridSize to client
	CSPPacketTypeTile        = 6  // Server sends individual tile data
	CSPPacketTypeTileReq     = 7  // Client requests missing tiles
	CSPPacketTypeAudioInfo   = 8  // Server sends audio format metadata to client
	CSPPacketTypeAudioData   = 9  // Server sends encoded audio payload
	CSPPacketTypeProbe       = 10 // Client probes RTT
	CSPPacketTypeProbeReply  = 11 // Server echoes probe
	CSPPacketTypeFEC         = 12 // XOR FEC parity packet
	CSPPacketTypeKeyframeReq = 13 // Client requests instantaneous keyframe (PLI)

	// Media payloads leave enough room for a 16-packet XOR FEC descriptor and
	// parity bytes while remaining under CSPMaxPacketSize / a safe Ethernet MTU.
	CSPMediaPayloadSize = 1360
	CSPMaxFECGroupSize  = 16
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
	if buf[0] != CSPVersion {
		return fmt.Errorf("unsupported CSP version: got %d want %d", buf[0], CSPVersion)
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

// ControlFeedback is congestion and network delivery feedback sent from client to server.
type ControlFeedback struct {
	FrameQueuePercent    uint8
	AudioQueuePercent    uint8
	RTTMS                uint16
	FrameDrops           uint32
	AudioDrops           uint32
	NACKSent             uint32
	JitterMS             uint16
	LossPermille         uint16
	ResidualLossPermille uint16
	DeliveryRateKbps     uint32
}

const ControlFeedbackPayloadSize = 28

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
	buf := make([]byte, CSPHeaderSize+ControlFeedbackPayloadSize)
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeControl,
	}
	h.Marshal(buf[:CSPHeaderSize])
	buf[CSPHeaderSize] = f.FrameQueuePercent
	buf[CSPHeaderSize+1] = f.AudioQueuePercent
	binary.BigEndian.PutUint16(buf[CSPHeaderSize+2:CSPHeaderSize+4], f.RTTMS)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+4:CSPHeaderSize+8], f.FrameDrops)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+8:CSPHeaderSize+12], f.AudioDrops)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+12:CSPHeaderSize+16], f.NACKSent)
	binary.BigEndian.PutUint16(buf[CSPHeaderSize+16:CSPHeaderSize+18], f.JitterMS)
	binary.BigEndian.PutUint16(buf[CSPHeaderSize+18:CSPHeaderSize+20], f.LossPermille)
	binary.BigEndian.PutUint16(buf[CSPHeaderSize+20:CSPHeaderSize+22], f.ResidualLossPermille)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+24:CSPHeaderSize+28], f.DeliveryRateKbps)
	return buf
}

// UnmarshalControlFeedback deserializes client congestion feedback.
func UnmarshalControlFeedback(buf []byte) (ControlFeedback, error) {
	if len(buf) < CSPHeaderSize+ControlFeedbackPayloadSize {
		return ControlFeedback{}, fmt.Errorf("buffer too small for control feedback: %d (want %d)", len(buf), CSPHeaderSize+ControlFeedbackPayloadSize)
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return ControlFeedback{}, err
	}
	if h.PacketType != CSPPacketTypeControl {
		return ControlFeedback{}, fmt.Errorf("not a control packet: %d", h.PacketType)
	}

	return ControlFeedback{
		FrameQueuePercent:    buf[CSPHeaderSize],
		AudioQueuePercent:    buf[CSPHeaderSize+1],
		RTTMS:                binary.BigEndian.Uint16(buf[CSPHeaderSize+2 : CSPHeaderSize+4]),
		FrameDrops:           binary.BigEndian.Uint32(buf[CSPHeaderSize+4 : CSPHeaderSize+8]),
		AudioDrops:           binary.BigEndian.Uint32(buf[CSPHeaderSize+8 : CSPHeaderSize+12]),
		NACKSent:             binary.BigEndian.Uint32(buf[CSPHeaderSize+12 : CSPHeaderSize+16]),
		JitterMS:             binary.BigEndian.Uint16(buf[CSPHeaderSize+16 : CSPHeaderSize+18]),
		LossPermille:         binary.BigEndian.Uint16(buf[CSPHeaderSize+18 : CSPHeaderSize+20]),
		ResidualLossPermille: binary.BigEndian.Uint16(buf[CSPHeaderSize+20 : CSPHeaderSize+22]),
		DeliveryRateKbps:     binary.BigEndian.Uint32(buf[CSPHeaderSize+24 : CSPHeaderSize+28]),
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

// MarshalKeyframeRequest serializes an upstream keyframe/PLI request.
func MarshalKeyframeRequest(reason string) []byte {
	buf := make([]byte, CSPHeaderSize+1+len(reason))
	h := PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeKeyframeReq,
		Timestamp:  NowTimestampMS(),
	}
	h.Marshal(buf[:CSPHeaderSize])
	buf[CSPHeaderSize] = uint8(len(reason))
	copy(buf[CSPHeaderSize+1:], []byte(reason))
	return buf
}

// UnmarshalKeyframeRequest deserializes an upstream keyframe request.
func UnmarshalKeyframeRequest(buf []byte) (reason string, err error) {
	if len(buf) < CSPHeaderSize {
		return "", fmt.Errorf("buffer too small for KeyframeRequest: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return "", err
	}
	if h.PacketType != CSPPacketTypeKeyframeReq {
		return "", fmt.Errorf("not a KeyframeRequest packet: %d", h.PacketType)
	}
	if len(buf) > CSPHeaderSize {
		reasonLen := int(buf[CSPHeaderSize])
		if len(buf) >= CSPHeaderSize+1+reasonLen {
			reason = string(buf[CSPHeaderSize+1 : CSPHeaderSize+1+reasonLen])
		}
	}
	return reason, nil
}

type XORFEC struct {
	FrameSeq     uint32
	GroupStart   uint32
	TotalPackets uint32
	Timestamp    uint32
	Lengths      []uint16
	Parity       []byte
}

// NowTimestampMS returns a wrapping 32-bit wall-clock timestamp. Subtraction
// on uint32 intentionally preserves short age measurements across wraparound.
func NowTimestampMS() uint32 {
	return uint32(time.Now().UnixMilli())
}

func TimestampAgeMS(timestamp uint32) uint32 {
	return NowTimestampMS() - timestamp
}

func MarshalProbe(nonce, sentAt uint32) []byte {
	buf := make([]byte, CSPHeaderSize)
	h := PacketHeader{Version: CSPVersion, PacketType: CSPPacketTypeProbe, FrameSeq: nonce, Timestamp: sentAt}
	h.Marshal(buf)
	return buf
}

func MarshalProbeReply(nonce, sentAt uint32) []byte {
	buf := make([]byte, CSPHeaderSize)
	h := PacketHeader{Version: CSPVersion, PacketType: CSPPacketTypeProbeReply, FrameSeq: nonce, Timestamp: sentAt}
	h.Marshal(buf)
	return buf
}

// MarshalXORFEC creates one parity packet that can recover any single missing
// media payload in a group. PacketID stores the first protected media packet
// ID; TotalPackets remains the original media frame fragment count.
func MarshalXORFEC(frameSeq, groupStart, totalPackets, timestamp uint32, payloads [][]byte) ([]byte, error) {
	if len(payloads) < 2 || len(payloads) > CSPMaxFECGroupSize {
		return nil, fmt.Errorf("FEC group size must be between 2 and %d", CSPMaxFECGroupSize)
	}
	maxLen := 0
	lengths := make([]uint16, len(payloads))
	for i, payload := range payloads {
		if len(payload) > CSPMediaPayloadSize || len(payload) > int(^uint16(0)) {
			return nil, fmt.Errorf("FEC payload %d too large: %d", i, len(payload))
		}
		lengths[i] = uint16(len(payload))
		if len(payload) > maxLen {
			maxLen = len(payload)
		}
	}

	parity := make([]byte, maxLen)
	for _, payload := range payloads {
		for i, b := range payload {
			parity[i] ^= b
		}
	}

	metadataLen := 2 + len(lengths)*2
	buf := make([]byte, CSPHeaderSize+metadataLen+len(parity))
	if len(buf) > CSPMaxPacketSize {
		return nil, fmt.Errorf("FEC packet exceeds max size: %d", len(buf))
	}
	h := PacketHeader{
		Version:      CSPVersion,
		PacketType:   CSPPacketTypeFEC,
		FrameSeq:     frameSeq,
		PacketID:     groupStart,
		TotalPackets: totalPackets,
		Timestamp:    timestamp,
	}
	h.Marshal(buf[:CSPHeaderSize])
	binary.BigEndian.PutUint16(buf[CSPHeaderSize:CSPHeaderSize+2], uint16(len(lengths)))
	pos := CSPHeaderSize + 2
	for _, length := range lengths {
		binary.BigEndian.PutUint16(buf[pos:pos+2], length)
		pos += 2
	}
	copy(buf[pos:], parity)
	return buf, nil
}

func UnmarshalXORFEC(packet []byte) (XORFEC, error) {
	if len(packet) < CSPHeaderSize+6 {
		return XORFEC{}, fmt.Errorf("FEC packet too small: %d", len(packet))
	}
	var h PacketHeader
	if err := h.Unmarshal(packet[:CSPHeaderSize]); err != nil {
		return XORFEC{}, err
	}
	if h.PacketType != CSPPacketTypeFEC {
		return XORFEC{}, fmt.Errorf("not an FEC packet")
	}
	count := int(binary.BigEndian.Uint16(packet[CSPHeaderSize : CSPHeaderSize+2]))
	if count < 2 || count > CSPMaxFECGroupSize {
		return XORFEC{}, fmt.Errorf("invalid FEC group size: %d", count)
	}
	metadataEnd := CSPHeaderSize + 2 + count*2
	if metadataEnd > len(packet) {
		return XORFEC{}, fmt.Errorf("truncated FEC lengths")
	}
	lengths := make([]uint16, count)
	pos := CSPHeaderSize + 2
	maxLen := 0
	for i := range lengths {
		lengths[i] = binary.BigEndian.Uint16(packet[pos : pos+2])
		if int(lengths[i]) > maxLen {
			maxLen = int(lengths[i])
		}
		pos += 2
	}
	parity := packet[metadataEnd:]
	if len(parity) != maxLen {
		return XORFEC{}, fmt.Errorf("FEC parity length=%d want=%d", len(parity), maxLen)
	}
	return XORFEC{
		FrameSeq:     h.FrameSeq,
		GroupStart:   h.PacketID,
		TotalPackets: h.TotalPackets,
		Timestamp:    h.Timestamp,
		Lengths:      lengths,
		Parity:       append([]byte(nil), parity...),
	}, nil
}

// BuildXORFEC protects contiguous CSP data packets with one parity packet per
// group. It expects already-fragmented CSP DATA packets.
func BuildXORFEC(dataPackets [][]byte, groupSize int) ([][]byte, error) {
	if groupSize < 2 || groupSize > CSPMaxFECGroupSize || len(dataPackets) < 2 {
		return nil, nil
	}
	fecPackets := make([][]byte, 0, (len(dataPackets)+groupSize-1)/groupSize)
	for start := 0; start < len(dataPackets); start += groupSize {
		end := start + groupSize
		if end > len(dataPackets) {
			end = len(dataPackets)
		}
		if end-start < 2 {
			continue
		}

		var first PacketHeader
		if len(dataPackets[start]) < CSPHeaderSize {
			return nil, fmt.Errorf("data packet %d too small", start)
		}
		if err := first.Unmarshal(dataPackets[start][:CSPHeaderSize]); err != nil {
			return nil, err
		}
		if first.PacketType != CSPPacketTypeData {
			return nil, fmt.Errorf("packet %d is not DATA", start)
		}
		payloads := make([][]byte, 0, end-start)
		for i := start; i < end; i++ {
			if len(dataPackets[i]) < CSPHeaderSize {
				return nil, fmt.Errorf("data packet %d too small", i)
			}
			var h PacketHeader
			if err := h.Unmarshal(dataPackets[i][:CSPHeaderSize]); err != nil {
				return nil, err
			}
			if h.PacketType != CSPPacketTypeData || h.FrameSeq != first.FrameSeq || h.PacketID != first.PacketID+uint32(i-start) {
				return nil, fmt.Errorf("non-contiguous DATA packet at index %d", i)
			}
			payloads = append(payloads, dataPackets[i][CSPHeaderSize:])
		}
		fec, err := MarshalXORFEC(first.FrameSeq, first.PacketID, first.TotalPackets, first.Timestamp, payloads)
		if err != nil {
			return nil, err
		}
		fecPackets = append(fecPackets, fec)
	}
	return fecPackets, nil
}
