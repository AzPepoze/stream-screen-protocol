package stream

import (
	"encoding/binary"
	"fmt"
	"time"
)

const (
	CSPPacketTypeProbe      = 10
	CSPPacketTypeProbeReply = 11
	CSPPacketTypeFEC        = 12

	// Media payloads leave enough room for a 16-packet XOR FEC descriptor and
	// parity bytes while remaining under CSPMaxPacketSize / a safe Ethernet MTU.
	CSPMediaPayloadSize = 1360
	CSPMaxFECGroupSize  = 16
)

// ExtendedControlFeedback keeps the original queue/drop feedback and adds
// network measurements that can drive per-viewer pacing decisions.
type ExtendedControlFeedback struct {
	ControlFeedback
	RTTMS            uint16
	JitterMS         uint16
	LossPermille     uint16
	DeliveryRateKbps uint32
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

// MarshalExtendedControlFeedback uses payload version 2 while keeping the
// original CSP header and v1 fields at their existing offsets.
func MarshalExtendedControlFeedback(f ExtendedControlFeedback) []byte {
	const payloadSize = 28
	buf := make([]byte, CSPHeaderSize+payloadSize)
	h := PacketHeader{Version: CSPVersion, PacketType: CSPPacketTypeControl}
	h.Marshal(buf[:CSPHeaderSize])
	buf[CSPHeaderSize] = 2
	buf[CSPHeaderSize+1] = f.FrameQueuePercent
	buf[CSPHeaderSize+2] = f.AudioQueuePercent
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+4:CSPHeaderSize+8], f.FrameDrops)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+8:CSPHeaderSize+12], f.AudioDrops)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+12:CSPHeaderSize+16], f.NACKSent)
	binary.BigEndian.PutUint16(buf[CSPHeaderSize+16:CSPHeaderSize+18], f.RTTMS)
	binary.BigEndian.PutUint16(buf[CSPHeaderSize+18:CSPHeaderSize+20], f.JitterMS)
	binary.BigEndian.PutUint16(buf[CSPHeaderSize+20:CSPHeaderSize+22], f.LossPermille)
	binary.BigEndian.PutUint32(buf[CSPHeaderSize+24:CSPHeaderSize+28], f.DeliveryRateKbps)
	return buf
}

// UnmarshalExtendedControlFeedback accepts both v1 and v2 feedback so older
// clients can still connect while the server gains richer measurements.
func UnmarshalExtendedControlFeedback(buf []byte) (ExtendedControlFeedback, error) {
	if len(buf) < CSPHeaderSize+16 {
		return ExtendedControlFeedback{}, fmt.Errorf("buffer too small for control feedback: %d", len(buf))
	}
	var h PacketHeader
	if err := h.Unmarshal(buf[:CSPHeaderSize]); err != nil {
		return ExtendedControlFeedback{}, err
	}
	if h.PacketType != CSPPacketTypeControl {
		return ExtendedControlFeedback{}, fmt.Errorf("not a control packet")
	}

	version := buf[CSPHeaderSize]
	if version == 1 {
		legacy, err := UnmarshalControlFeedback(buf)
		if err != nil {
			return ExtendedControlFeedback{}, err
		}
		return ExtendedControlFeedback{ControlFeedback: legacy}, nil
	}
	if version != 2 {
		return ExtendedControlFeedback{}, fmt.Errorf("unsupported control payload version: %d", version)
	}
	if len(buf) < CSPHeaderSize+28 {
		return ExtendedControlFeedback{}, fmt.Errorf("buffer too small for v2 control feedback: %d", len(buf))
	}

	return ExtendedControlFeedback{
		ControlFeedback: ControlFeedback{
			FrameQueuePercent: buf[CSPHeaderSize+1],
			AudioQueuePercent: buf[CSPHeaderSize+2],
			FrameDrops:        binary.BigEndian.Uint32(buf[CSPHeaderSize+4 : CSPHeaderSize+8]),
			AudioDrops:        binary.BigEndian.Uint32(buf[CSPHeaderSize+8 : CSPHeaderSize+12]),
			NACKSent:          binary.BigEndian.Uint32(buf[CSPHeaderSize+12 : CSPHeaderSize+16]),
		},
		RTTMS:            binary.BigEndian.Uint16(buf[CSPHeaderSize+16 : CSPHeaderSize+18]),
		JitterMS:         binary.BigEndian.Uint16(buf[CSPHeaderSize+18 : CSPHeaderSize+20]),
		LossPermille:     binary.BigEndian.Uint16(buf[CSPHeaderSize+20 : CSPHeaderHeaderSize+22]),
		DeliveryRateKbps: binary.BigEndian.Uint32(buf[CSPHeaderSize+24 : CSPHeaderSize+28]),
	}, nil
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
