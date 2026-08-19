package rtpffmpeg

import (
	"encoding/binary"
	"testing"
)

func TestParseRTPPacket(t *testing.T) {
	packet := make([]byte, 12+3)
	packet[0] = 0x80
	packet[1] = 0x80 | 96
	binary.BigEndian.PutUint16(packet[2:4], 7)
	binary.BigEndian.PutUint32(packet[4:8], 1234)
	copy(packet[12:], []byte{0x65, 0xaa, 0xbb})

	headerLen, marker, ts, payload, ok := parseRTPPacket(packet)
	if !ok {
		t.Fatal("packet should parse")
	}
	if headerLen != 12 || !marker || ts != 1234 {
		t.Fatalf("unexpected RTP header: len=%d marker=%v ts=%d", headerLen, marker, ts)
	}
	if len(payload) != 3 || payload[0] != 0x65 {
		t.Fatalf("unexpected payload: %x", payload)
	}
}

func TestAppendSingleNAL(t *testing.T) {
	got, err := appendH264RTPPayload(nil, []byte{0x65, 0x01, 0x02})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 1, 0x65, 0x01, 0x02}
	if string(got) != string(want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestAppendSTAPA(t *testing.T) {
	payload := []byte{24, 0, 2, 0x67, 0x11, 0, 3, 0x68, 0x22, 0x33}
	got, err := appendH264RTPPayload(nil, payload)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 1, 0x67, 0x11, 0, 0, 0, 1, 0x68, 0x22, 0x33}
	if string(got) != string(want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestAppendFUA(t *testing.T) {
	var got []byte
	var err error
	got, err = appendH264RTPPayload(got, []byte{0x7c, 0x85, 0xaa, 0xbb}) // start IDR
	if err != nil {
		t.Fatal(err)
	}
	got, err = appendH264RTPPayload(got, []byte{0x7c, 0x45, 0xcc}) // end IDR
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 1, 0x65, 0xaa, 0xbb, 0xcc}
	if string(got) != string(want) {
		t.Fatalf("got %x want %x", got, want)
	}
}
