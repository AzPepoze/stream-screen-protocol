package client

import (
	"testing"
	"time"

	"streamscreen/internal/video/stream"
)

func TestAutoJitterTimingHighRefresh(t *testing.T) {
	tests := []struct {
		fps      int
		wantMax  time.Duration
		wantNACK time.Duration
	}{
		{fps: 60, wantMax: 4 * (time.Second / 60), wantNACK: (time.Second / 60) / 2},
		{fps: 120, wantMax: 4 * (time.Second / 120), wantNACK: (time.Second / 120) / 2},
		{fps: 240, wantMax: 25 * time.Millisecond, wantNACK: 3 * time.Millisecond},
	}
	for _, tt := range tests {
		gotMax, gotNACK := autoJitterTiming(tt.fps, 200*time.Millisecond, 20*time.Millisecond)
		if gotMax != tt.wantMax || gotNACK != tt.wantNACK {
			t.Fatalf("fps=%d got max=%s nack=%s want max=%s nack=%s", tt.fps, gotMax, gotNACK, tt.wantMax, tt.wantNACK)
		}
	}
}

func TestAutoJitterTimingHonorsLowerUserCaps(t *testing.T) {
	maxLatency, nack := autoJitterTiming(60, 40*time.Millisecond, 4*time.Millisecond)
	if maxLatency != 40*time.Millisecond || nack != 4*time.Millisecond {
		t.Fatalf("got max=%s nack=%s", maxLatency, nack)
	}
}

func TestJitterBufferNACKsBeforeExpiration(t *testing.T) {
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        50 * time.Millisecond,
		NackRetryDelay:    5 * time.Millisecond,
		PartialFrameReady: 1,
		AllowPartial:      false,
		ForceOutput:       false,
	})
	header := stream.PacketHeader{FrameSeq: 10, PacketID: 0, TotalPackets: 3}
	jb.Push(header, []byte("a"))

	jb.mu.Lock()
	jb.frames[10].receivedAt = time.Now().Add(-10 * time.Millisecond)
	jb.mu.Unlock()
	jb.Push(header, []byte("a"))

	select {
	case req := <-jb.nackChan:
		if req.FrameSeq != 10 || len(req.PacketIDs) != 2 || req.PacketIDs[0] != 1 || req.PacketIDs[1] != 2 {
			t.Fatalf("unexpected NACK: %#v", req)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected early NACK before maxLatency")
	}
}
