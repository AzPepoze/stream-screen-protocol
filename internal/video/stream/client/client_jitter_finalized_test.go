package client

import (
	"testing"
	"time"

	"streamscreen/internal/video/stream"
)

func TestFinalizedFrameIgnoresLateDuplicate(t *testing.T) {
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        50 * time.Millisecond,
		NackRetryDelay:    5 * time.Millisecond,
		PartialFrameReady: 1.0,
		AllowPartial:      false,
		ForceOutput:       false,
	})

	header0 := stream.PacketHeader{FrameSeq: 100, PacketID: 0, TotalPackets: 2}
	header1 := stream.PacketHeader{FrameSeq: 100, PacketID: 1, TotalPackets: 2}
	jb.Push(header0, []byte("a"))
	ready, seq := jb.Push(header1, []byte("b"))
	if ready == nil || seq != 100 {
		t.Fatalf("expected frame 100 to finalize")
	}

	// A delayed retransmission must not create a new FrameBuffer for seq 100.
	jb.Push(header0, []byte("late"))
	jb.mu.Lock()
	_, resurrected := jb.frames[100]
	jb.mu.Unlock()
	if resurrected {
		t.Fatal("late packet resurrected finalized frame 100")
	}
}

func TestDuplicatePacketDoesNotResetReorderQuietTimer(t *testing.T) {
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        50 * time.Millisecond,
		NackRetryDelay:    5 * time.Millisecond,
		PartialFrameReady: 1.0,
		AllowPartial:      false,
		ForceOutput:       false,
	})

	header := stream.PacketHeader{FrameSeq: 101, PacketID: 0, TotalPackets: 3}
	jb.Push(header, []byte("a"))
	jb.mu.Lock()
	before := jb.frames[101].lastPacketAt
	jb.mu.Unlock()

	time.Sleep(time.Millisecond)
	jb.Push(header, []byte("duplicate"))
	jb.mu.Lock()
	after := jb.frames[101].lastPacketAt
	jb.mu.Unlock()
	if !after.Equal(before) {
		t.Fatalf("duplicate reset quiet timer: before=%s after=%s", before, after)
	}
}
