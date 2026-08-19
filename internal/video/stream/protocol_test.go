package stream

import (
	"bytes"
	"testing"
)

func TestControlFeedbackRoundTrip(t *testing.T) {
	want := ControlFeedback{
		FrameQueuePercent:    42,
		AudioQueuePercent:    7,
		RTTMS:                55,
		FrameDrops:           3,
		AudioDrops:           1,
		NACKSent:             9,
		JitterMS:             6,
		LossPermille:         27,
		ResidualLossPermille: 12,
		DeliveryRateKbps:     18432,
	}
	got, err := UnmarshalControlFeedback(MarshalControlFeedback(want))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestControlFeedbackMalformedPayloads(t *testing.T) {
	valid := MarshalControlFeedback(ControlFeedback{
		FrameQueuePercent: 50,
		RTTMS:             30,
	})

	// 1. Buffer smaller than CSPHeaderSize
	if _, err := UnmarshalControlFeedback(valid[:CSPHeaderSize-1]); err == nil {
		t.Fatal("expected error on buffer smaller than CSP header")
	}

	// 2. Buffer smaller than CSPHeaderSize + ControlFeedbackPayloadSize
	if _, err := UnmarshalControlFeedback(valid[:len(valid)-1]); err == nil {
		t.Fatal("expected error on truncated payload")
	}

	// 3. Wrong version in header
	wrongVer := make([]byte, len(valid))
	copy(wrongVer, valid)
	wrongVer[0] = 99
	if _, err := UnmarshalControlFeedback(wrongVer); err == nil {
		t.Fatal("expected error on mismatched protocol version")
	}

	// 4. Wrong packet type
	wrongType := make([]byte, len(valid))
	copy(wrongType, valid)
	wrongType[1] = CSPPacketTypeData
	if _, err := UnmarshalControlFeedback(wrongType); err == nil {
		t.Fatal("expected error on wrong packet type")
	}
}

func TestProbeRoundTripHeader(t *testing.T) {
	packet := MarshalProbe(123, 456)
	var h PacketHeader
	if err := h.Unmarshal(packet); err != nil {
		t.Fatal(err)
	}
	if h.PacketType != CSPPacketTypeProbe || h.FrameSeq != 123 || h.Timestamp != 456 {
		t.Fatalf("unexpected probe header: %#v", h)
	}
}

func TestXORFECRoundTrip(t *testing.T) {
	payloads := [][]byte{
		{1, 2, 3, 4},
		{10, 20},
		{5, 6, 7},
	}
	packet, err := MarshalXORFEC(9, 12, 20, 12345, payloads)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) > CSPMaxPacketSize {
		t.Fatalf("FEC packet=%d exceeds max=%d", len(packet), CSPMaxPacketSize)
	}
	fec, err := UnmarshalXORFEC(packet)
	if err != nil {
		t.Fatal(err)
	}
	if fec.FrameSeq != 9 || fec.GroupStart != 12 || fec.TotalPackets != 20 || fec.Timestamp != 12345 {
		t.Fatalf("unexpected FEC metadata: %#v", fec)
	}
	if len(fec.Lengths) != 3 || fec.Lengths[0] != 4 || fec.Lengths[1] != 2 || fec.Lengths[2] != 3 {
		t.Fatalf("unexpected FEC lengths: %v", fec.Lengths)
	}
	wantParity := []byte{1 ^ 10 ^ 5, 2 ^ 20 ^ 6, 3 ^ 0 ^ 7, 4}
	if !bytes.Equal(fec.Parity, wantParity) {
		t.Fatalf("parity=%v want=%v", fec.Parity, wantParity)
	}
}

func TestKeyframeRequestRoundTrip(t *testing.T) {
	reason := "decode_error"
	packet := MarshalKeyframeRequest(reason)
	gotReason, err := UnmarshalKeyframeRequest(packet)
	if err != nil {
		t.Fatalf("failed to unmarshal keyframe request: %v", err)
	}
	if gotReason != reason {
		t.Fatalf("expected reason %q, got %q", reason, gotReason)
	}
}

func TestBuildXORFECFromDataPackets(t *testing.T) {
	payloads := [][]byte{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}}
	packets := make([][]byte, 0, len(payloads))
	for packetID, payload := range payloads {
		buf := make([]byte, CSPHeaderSize+len(payload))
		h := PacketHeader{
			Version:      CSPVersion,
			PacketType:   CSPPacketTypeData,
			FrameSeq:     77,
			PacketID:     uint32(packetID),
			TotalPackets: uint32(len(payloads)),
			Timestamp:    88,
		}
		h.Marshal(buf[:CSPHeaderSize])
		copy(buf[CSPHeaderSize:], payload)
		packets = append(packets, buf)
	}

	fecPackets, err := BuildXORFEC(packets, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(fecPackets) != 2 { // last one-packet tail is intentionally unprotected
		t.Fatalf("got %d FEC packets want=2", len(fecPackets))
	}
	first, err := UnmarshalXORFEC(fecPackets[0])
	if err != nil {
		t.Fatal(err)
	}
	if first.GroupStart != 0 || len(first.Lengths) != 2 {
		t.Fatalf("unexpected first FEC group: %#v", first)
	}
	second, err := UnmarshalXORFEC(fecPackets[1])
	if err != nil {
		t.Fatal(err)
	}
	if second.GroupStart != 2 || len(second.Lengths) != 2 {
		t.Fatalf("unexpected second FEC group: %#v", second)
	}
}
