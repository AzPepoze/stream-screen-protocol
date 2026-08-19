package client

import (
	"testing"
	"time"

	"streamscreen/internal/video/stream"
)

type testLossTracker struct {
	uniqueDetected int
	recoveredNACK  int
	recoveredFEC   int
	unrecovered    int
}

func (t *testLossTracker) OnUniqueMissingDetected(count int) {
	t.uniqueDetected += count
}

func (t *testLossTracker) OnMissingRecoveredByNACK(count int) {
	t.recoveredNACK += count
}

func (t *testLossTracker) OnMissingRecoveredByFEC(count int) {
	t.recoveredFEC += count
}

func (t *testLossTracker) OnMissingUnrecovered(count int) {
	t.unrecovered += count
}

// 1. Test that the same missing packet being NACKed repeatedly does not inflate unique loss.
func TestRepeatedNACKRetriesDoNotInflateUniqueLoss(t *testing.T) {
	tracker := &testLossTracker{}
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        50 * time.Millisecond,
		NackRetryDelay:    5 * time.Millisecond,
		PartialFrameReady: 1.0,
		AllowPartial:      false,
		ForceOutput:       false,
		LossObserver:      tracker,
	})

	// Push packet 0 of 3 (packets 1 and 2 are missing)
	header := stream.PacketHeader{FrameSeq: 10, PacketID: 0, TotalPackets: 3}
	jb.Push(header, []byte("data-0"))

	if tracker.uniqueDetected != 2 {
		t.Fatalf("expected 2 unique missing packets detected, got %d", tracker.uniqueDetected)
	}

	// Trigger 4 subsequent NACK checks by simulating time passage and pushing
	for i := 0; i < 4; i++ {
		jb.mu.Lock()
		jb.frames[10].receivedAt = time.Now().Add(-10 * time.Millisecond)
		delete(jb.nackedFrames, 10)
		jb.mu.Unlock()

		jb.Push(header, []byte("data-0"))
	}

	// uniqueDetected must STILL be exactly 2
	if tracker.uniqueDetected != 2 {
		t.Fatalf("uniqueDetected inflated by NACK retries: got %d want 2", tracker.uniqueDetected)
	}
}

// 2. Test missing packet recovered by NACK
func TestMissingPacketRecoveredByNACK(t *testing.T) {
	tracker := &testLossTracker{}
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        100 * time.Millisecond,
		NackRetryDelay:    10 * time.Millisecond,
		PartialFrameReady: 1.0,
		AllowPartial:      false,
		ForceOutput:       false,
		LossObserver:      tracker,
	})

	// Frame with 3 packets, packet 0 arrives
	jb.Push(stream.PacketHeader{FrameSeq: 20, PacketID: 0, TotalPackets: 3}, []byte("p0"))
	if tracker.uniqueDetected != 2 {
		t.Fatalf("uniqueDetected got %d want 2", tracker.uniqueDetected)
	}

	// Packet 1 arrives via NACK response
	jb.Push(stream.PacketHeader{FrameSeq: 20, PacketID: 1, TotalPackets: 3}, []byte("p1"))
	if tracker.recoveredNACK != 1 {
		t.Fatalf("recoveredNACK got %d want 1", tracker.recoveredNACK)
	}

	// Packet 2 arrives, completing the frame
	ready, seq := jb.Push(stream.PacketHeader{FrameSeq: 20, PacketID: 2, TotalPackets: 3}, []byte("p2"))
	if ready == nil || seq != 20 {
		t.Fatalf("expected frame 20 ready, got %v seq=%d", ready != nil, seq)
	}
	if tracker.recoveredNACK != 2 {
		t.Fatalf("recoveredNACK got %d want 2", tracker.recoveredNACK)
	}
	if tracker.unrecovered != 0 {
		t.Fatalf("unrecovered got %d want 0", tracker.unrecovered)
	}
}

// 3. Test missing packet recovered by FEC
func TestMissingPacketRecoveredByFEC(t *testing.T) {
	tracker := &testLossTracker{}
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        100 * time.Millisecond,
		NackRetryDelay:    10 * time.Millisecond,
		PartialFrameReady: 1.0,
		AllowPartial:      false,
		ForceOutput:       false,
		LossObserver:      tracker,
	})

	// Frame 30 has 2 packets. Packet 0 arrives.
	jb.Push(stream.PacketHeader{FrameSeq: 30, PacketID: 0, TotalPackets: 2}, []byte("a"))
	if tracker.uniqueDetected != 1 {
		t.Fatalf("uniqueDetected got %d want 1", tracker.uniqueDetected)
	}

	// Packet 1 recovered by FEC
	jb.MarkRecoveredByFEC(30, 1)
	if tracker.recoveredFEC != 1 {
		t.Fatalf("recoveredFEC got %d want 1", tracker.recoveredFEC)
	}

	// Pushing the FEC-recovered packet should complete frame without double counting NACK recovery
	ready, seq := jb.Push(stream.PacketHeader{FrameSeq: 30, PacketID: 1, TotalPackets: 2}, []byte("b"))
	if ready == nil || seq != 30 {
		t.Fatalf("expected frame 30 ready")
	}
	if tracker.recoveredNACK != 0 {
		t.Fatalf("recoveredNACK should be 0, got %d", tracker.recoveredNACK)
	}
}

// 4. Test packet detected by both mechanisms without double counting
func TestPacketDetectedByBothWithoutDoubleCounting(t *testing.T) {
	tracker := &testLossTracker{}
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        100 * time.Millisecond,
		NackRetryDelay:    10 * time.Millisecond,
		PartialFrameReady: 1.0,
		AllowPartial:      false,
		ForceOutput:       false,
		LossObserver:      tracker,
	})

	// Frame 40 has 3 packets. Packet 0 arrives.
	jb.Push(stream.PacketHeader{FrameSeq: 40, PacketID: 0, TotalPackets: 3}, []byte("x"))
	if tracker.uniqueDetected != 2 {
		t.Fatalf("uniqueDetected got %d want 2", tracker.uniqueDetected)
	}

	// Mark packet 1 recovered by FEC first
	jb.MarkRecoveredByFEC(40, 1)
	if tracker.recoveredFEC != 1 {
		t.Fatalf("recoveredFEC got %d want 1", tracker.recoveredFEC)
	}

	// Now packet 1 also arrives over UDP (e.g. duplicate / delayed NACK)
	jb.Push(stream.PacketHeader{FrameSeq: 40, PacketID: 1, TotalPackets: 3}, []byte("y"))

	// Should not increment recoveredNACK since already recovered by FEC
	if tracker.recoveredNACK != 0 {
		t.Fatalf("recoveredNACK got %d want 0 (no double counting)", tracker.recoveredNACK)
	}
	if tracker.recoveredFEC != 1 {
		t.Fatalf("recoveredFEC got %d want 1", tracker.recoveredFEC)
	}
}

// 5. Test expired/unrecoverable packet
func TestExpiredUnrecoverablePacket(t *testing.T) {
	tracker := &testLossTracker{}
	jb := NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        20 * time.Millisecond,
		NackRetryDelay:    5 * time.Millisecond,
		PartialFrameReady: 1.0,
		AllowPartial:      false,
		ForceOutput:       false,
		LossObserver:      tracker,
	})

	// Frame 50 has 2 packets, only packet 0 arrives
	jb.Push(stream.PacketHeader{FrameSeq: 50, PacketID: 0, TotalPackets: 2}, []byte("lone"))
	if tracker.uniqueDetected != 1 {
		t.Fatalf("uniqueDetected got %d want 1", tracker.uniqueDetected)
	}

	// Simulate expiration past 2*maxLatency
	jb.mu.Lock()
	jb.frames[50].receivedAt = time.Now().Add(-50 * time.Millisecond)
	jb.mu.Unlock()

	// Push new frame 51 to trigger cleanup
	jb.Push(stream.PacketHeader{FrameSeq: 51, PacketID: 0, TotalPackets: 1}, []byte("next"))

	if tracker.unrecovered != 1 {
		t.Fatalf("unrecovered got %d want 1", tracker.unrecovered)
	}
}
