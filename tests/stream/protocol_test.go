package stream_test

import (
	"testing"

	s "streamscreen/internal/video/stream"
)

func TestMarshalUnmarshalJoin(t *testing.T) {
	endpoint := "203.0.113.5:54321"
	b := s.MarshalJoin(endpoint)
	got, err := s.UnmarshalJoin(b)
	if err != nil {
		t.Fatalf("UnmarshalJoin returned error: %v", err)
	}
	if got != endpoint {
		t.Fatalf("expected %q, got %q", endpoint, got)
	}

	// empty join payload
	b2 := make([]byte, s.CSPHeaderSize)
	var h s.PacketHeader
	h.Version = s.CSPVersion
	h.PacketType = s.CSPPacketTypeJoin
	h.Marshal(b2)
	got2, err := s.UnmarshalJoin(b2)
	if err != nil {
		t.Fatalf("UnmarshalJoin(empty) returned error: %v", err)
	}
	if got2 != "" {
		t.Fatalf("expected empty join payload, got %q", got2)
	}
}

func TestMarshalUnmarshalAudioInfo(t *testing.T) {
	b := s.MarshalAudioInfo(48000, 2, 20, 96, "opus")
	sampleRate, channels, frameMS, bitrate, codec, err := s.UnmarshalAudioInfo(b)
	if err != nil {
		t.Fatalf("UnmarshalAudioInfo returned error: %v", err)
	}
	if sampleRate != 48000 || channels != 2 || frameMS != 20 || bitrate != 96 || codec != "opus" {
		t.Fatalf("unexpected AudioInfo values: rate=%d ch=%d frame=%d bitrate=%d codec=%q",
			sampleRate, channels, frameMS, bitrate, codec)
	}
}
