package stream

import (
	"encoding/binary"
	"fmt"
	"time"
)

const (
	CSPPacketTypeProbe      = 10
	CSPPacketTypeProbeReply = 11
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
		LossPermille:     binary.BigEndian.Uint16(buf[CSPHeaderSize+20 : CSPHeaderSize+22]),
		DeliveryRateKbps: binary.BigEndian.Uint32(buf[CSPHeaderSize+24 : CSPHeaderSize+28]),
	}, nil
}

func MarshalProbe(nonce, sentAt uint32) []byte {
	buf := make([]byte, CSPHeaderSize)
	PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeProbe,
		FrameSeq:   nonce,
		Timestamp:  sentAt,
	}.Marshal(buf)
	return buf
}

func MarshalProbeReply(nonce, sentAt uint32) []byte {
	buf := make([]byte, CSPHeaderSize)
	PacketHeader{
		Version:    CSPVersion,
		PacketType: CSPPacketTypeProbeReply,
		FrameSeq:   nonce,
		Timestamp:  sentAt,
	}.Marshal(buf)
	return buf
}
