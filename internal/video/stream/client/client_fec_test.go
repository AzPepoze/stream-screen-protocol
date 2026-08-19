package client

import (
	"bytes"
	"testing"
	"time"

	"streamscreen/internal/video/stream"
)

func TestFECRecovererRecoversOneMissingPacket(t *testing.T) {
	payloads := [][]byte{
		[]byte("packet-zero"),
		[]byte("packet-one-is-longer"),
		[]byte("packet-two"),
		[]byte("packet-three"),
	}
	fecPacket, err := stream.MarshalXORFEC(55, 0, 4, 1234, payloads)
	if err != nil {
		t.Fatal(err)
	}

	recoverer := NewFECRecoverer(time.Second)
	for packetID, payload := range payloads {
		if packetID == 1 {
			continue
		}
		h := stream.PacketHeader{
			Version:      stream.CSPVersion,
			PacketType:   stream.CSPPacketTypeData,
			FrameSeq:     55,
			PacketID:     uint32(packetID),
			TotalPackets: 4,
			Timestamp:    1234,
		}
		if recovered := recoverer.PushData(h, payload); len(recovered) != 0 {
			t.Fatalf("recovered before parity: %#v", recovered)
		}
	}

	recovered, err := recoverer.PushFEC(fecPacket)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 {
		t.Fatalf("recovered=%d want=1", len(recovered))
	}
	if recovered[0].Header.PacketID != 1 || recovered[0].Header.FrameSeq != 55 {
		t.Fatalf("unexpected recovered header: %#v", recovered[0].Header)
	}
	if !bytes.Equal(recovered[0].Payload, payloads[1]) {
		t.Fatalf("payload=%q want=%q", recovered[0].Payload, payloads[1])
	}
}

func TestFECRecovererDoesNotGuessTwoMissingPackets(t *testing.T) {
	payloads := [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")}
	fecPacket, err := stream.MarshalXORFEC(2, 0, 4, 7, payloads)
	if err != nil {
		t.Fatal(err)
	}
	recoverer := NewFECRecoverer(time.Second)
	for _, packetID := range []uint32{0, 3} {
		h := stream.PacketHeader{Version: stream.CSPVersion, PacketType: stream.CSPPacketTypeData, FrameSeq: 2, PacketID: packetID, TotalPackets: 4, Timestamp: 7}
		recoverer.PushData(h, payloads[packetID])
	}
	recovered, err := recoverer.PushFEC(fecPacket)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 0 {
		t.Fatalf("should not recover with two erasures: %#v", recovered)
	}
}

func TestFECRecovererHandlesParityBeforeLastData(t *testing.T) {
	payloads := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
	fecPacket, err := stream.MarshalXORFEC(3, 10, 20, 9, payloads)
	if err != nil {
		t.Fatal(err)
	}
	recoverer := NewFECRecoverer(time.Second)

	h0 := stream.PacketHeader{Version: stream.CSPVersion, PacketType: stream.CSPPacketTypeData, FrameSeq: 3, PacketID: 10, TotalPackets: 20, Timestamp: 9}
	recoverer.PushData(h0, payloads[0])
	if recovered, err := recoverer.PushFEC(fecPacket); err != nil || len(recovered) != 0 {
		t.Fatalf("parity should wait for enough data: recovered=%v err=%v", recovered, err)
	}

	h2 := stream.PacketHeader{Version: stream.CSPVersion, PacketType: stream.CSPPacketTypeData, FrameSeq: 3, PacketID: 12, TotalPackets: 20, Timestamp: 9}
	recovered := recoverer.PushData(h2, payloads[2])
	if len(recovered) != 1 || recovered[0].Header.PacketID != 11 || !bytes.Equal(recovered[0].Payload, payloads[1]) {
		t.Fatalf("unexpected recovery after reordered data: %#v", recovered)
	}
}
