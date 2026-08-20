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

func (t *testLossTracker) OnUniqueMissingDetected(count int) { t.uniqueDetected += count }
func (t *testLossTracker) OnMissingRecoveredByNACK(count int) { t.recoveredNACK += count }
func (t *testLossTracker) OnMissingRecoveredByFEC(count int) { t.recoveredFEC += count }
func (t *testLossTracker) OnMissingUnrecovered(count int) { t.unrecovered += count }

func newLossTestBuffer(tracker *testLossTracker) *JitterBuffer {
	return NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        50 * time.Millisecond,
		NackRetryDelay:    5 * time.Millisecond,
		PartialFrameReady: 1.0,
		AllowPartial:      false,
		ForceOutput:       false,
		LossObserver:      tracker,
	})
}

// Sequential packets that simply have not arrived yet are not packet loss.
// This is the regression that previously produced ~50% raw loss on a 0.5%
// netem link after the first fragment of every frame.
func TestFuturePacketsAreNotCountedAsLoss(t *testing.T) {
	tracker := &testLossTracker{}
	jb := newLossTestBuffer(tracker)

	for id := uint32(0); id < 3; id++ {
		ready, _ := jb.Push(stream.PacketHeader{FrameSeq: 1, PacketID: id, TotalPackets: 3}, []byte{byte(id)})
		if id < 2 && ready != nil {
			t.Fatalf("frame completed early at packet %d", id)
		}
		if tracker.uniqueDetected != 0 {
			t.Fatalf("packet %d caused false loss detection: %d", id, tracker.uniqueDetected)
		}
	}
}

func TestObservedGapBecomesUniqueLossOnce(t *testing.T) {
	tracker := &testLossTracker{}
	jb := newLossTestBuffer(tracker)

	jb.Push(stream.PacketHeader{FrameSeq: 10, PacketID: 0, TotalPackets: 3}, []byte("p0"))
	jb.Push(stream.PacketHeader{FrameSeq: 10, PacketID: 2, TotalPackets: 3}, []byte("p2"))
	if tracker.uniqueDetected != 0 {
		t.Fatalf("gap should wait for reorder grace, got %d", tracker.uniqueDetected)
	}

	jb.mu.Lock()
	jb.frames[10].lastPacketAt = time.Now().Add(-10 * time.Millisecond)
	jb.checkPendingFrames(time.Now(), 10)
	jb.mu.Unlock()
	if tracker.uniqueDetected != 1 {
		t.Fatalf("expected one observed missing packet, got %d", tracker.uniqueDetected)
	}
}

func TestRepeatedNACKRetriesDoNotInflateUniqueLoss(t *testing.T) {
	tracker := &testLossTracker{}
	jb := newLossTestBuffer(tracker)
	jb.Push(stream.PacketHeader{FrameSeq: 10, PacketID: 0, TotalPackets: 3}, []byte("p0"))
	jb.Push(stream.PacketHeader{FrameSeq: 10, PacketID: 2, TotalPackets: 3}, []byte("p2"))

	for i := 0; i < 4; i++ {
		jb.mu.Lock()
		jb.frames[10].lastPacketAt = time.Now().Add(-10 * time.Millisecond)
		jb.nackedFrames[10] = time.Now().Add(-10 * time.Millisecond)
		jb.checkPendingFrames(time.Now(), 10)
		jb.mu.Unlock()
	}
	if tracker.uniqueDetected != 1 {
		t.Fatalf("unique loss inflated by NACK retries: got %d want 1", tracker.uniqueDetected)
	}
}

func TestMissingPacketRecoveredAfterNACK(t *testing.T) {
	tracker := &testLossTracker{}
	jb := newLossTestBuffer(tracker)
	jb.Push(stream.PacketHeader{FrameSeq: 20, PacketID: 0, TotalPackets: 3}, []byte("p0"))
	jb.Push(stream.PacketHeader{FrameSeq: 20, PacketID: 2, TotalPackets: 3}, []byte("p2"))

	jb.mu.Lock()
	jb.frames[20].lastPacketAt = time.Now().Add(-10 * time.Millisecond)
	jb.checkPendingFrames(time.Now(), 20)
	jb.mu.Unlock()
	if tracker.uniqueDetected != 1 {
		t.Fatalf("uniqueDetected got %d want 1", tracker.uniqueDetected)
	}

	ready, seq := jb.Push(stream.PacketHeader{FrameSeq: 20, PacketID: 1, TotalPackets: 3}, []byte("p1"))
	if ready == nil || seq != 20 {
		t.Fatalf("expected frame 20 ready")
	}
	if tracker.recoveredNACK != 1 {
		t.Fatalf("recoveredNACK got %d want 1", tracker.recoveredNACK)
	}
	if tracker.unrecovered != 0 {
		t.Fatalf("unrecovered got %d want 0", tracker.unrecovered)
	}
}

func TestMissingPacketRecoveredByFEC(t *testing.T) {
	tracker := &testLossTracker{}
	jb := newLossTestBuffer(tracker)
	jb.Push(stream.PacketHeader{FrameSeq: 30, PacketID: 0, TotalPackets: 2}, []byte("a"))

	// FEC recovery itself is evidence that an original media packet was lost,
	// even when it happened before the NACK grace period elapsed.
	jb.MarkRecoveredByFEC(30, 1)
	if tracker.uniqueDetected != 1 {
		t.Fatalf("uniqueDetected got %d want 1", tracker.uniqueDetected)
	}
	if tracker.recoveredFEC != 1 {
		t.Fatalf("recoveredFEC got %d want 1", tracker.recoveredFEC)
	}

	ready, seq := jb.Push(stream.PacketHeader{FrameSeq: 30, PacketID: 1, TotalPackets: 2}, []byte("b"))
	if ready == nil || seq != 30 {
		t.Fatalf("expected frame 30 ready")
	}
	if tracker.recoveredNACK != 0 {
		t.Fatalf("FEC recovery was double counted as NACK recovery: %d", tracker.recoveredNACK)
	}
}

func TestPacketDetectedThenRecoveredByFECWithoutDoubleCounting(t *testing.T) {
	tracker := &testLossTracker{}
	jb := newLossTestBuffer(tracker)
	jb.Push(stream.PacketHeader{FrameSeq: 40, PacketID: 0, TotalPackets: 3}, []byte("x"))
	jb.Push(stream.PacketHeader{FrameSeq: 40, PacketID: 2, TotalPackets: 3}, []byte("z"))

	jb.mu.Lock()
	jb.frames[40].lastPacketAt = time.Now().Add(-10 * time.Millisecond)
	jb.checkPendingFrames(time.Now(), 40)
	jb.mu.Unlock()
	if tracker.uniqueDetected != 1 {
		t.Fatalf("uniqueDetected got %d want 1", tracker.uniqueDetected)
	}

	jb.MarkRecoveredByFEC(40, 1)
	jb.Push(stream.PacketHeader{FrameSeq: 40, PacketID: 1, TotalPackets: 3}, []byte("y"))
	if tracker.uniqueDetected != 1 || tracker.recoveredFEC != 1 || tracker.recoveredNACK != 0 {
		t.Fatalf("double counted mixed recovery: unique=%d fec=%d nack=%d", tracker.uniqueDetected, tracker.recoveredFEC, tracker.recoveredNACK)
	}
}

func TestTailLossDetectedAfterNewerFrame(t *testing.T) {
	tracker := &testLossTracker{}
	jb := newLossTestBuffer(tracker)
	jb.Push(stream.PacketHeader{FrameSeq: 50, PacketID: 0, TotalPackets: 2}, []byte("p0"))

	jb.mu.Lock()
	jb.frames[50].lastPacketAt = time.Now().Add(-10 * time.Millisecond)
	jb.mu.Unlock()
	jb.Push(stream.PacketHeader{FrameSeq: 51, PacketID: 0, TotalPackets: 1}, []byte("next"))

	if tracker.uniqueDetected != 1 {
		t.Fatalf("tail packet should be detected once newer frame proves frame closure, got %d", tracker.uniqueDetected)
	}
}

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
	jb.Push(stream.PacketHeader{FrameSeq: 60, PacketID: 0, TotalPackets: 2}, []byte("p0"))

	jb.mu.Lock()
	jb.frames[60].receivedAt = time.Now().Add(-50 * time.Millisecond)
	jb.frames[60].lastPacketAt = time.Now().Add(-50 * time.Millisecond)
	jb.mu.Unlock()
	jb.Push(stream.PacketHeader{FrameSeq: 61, PacketID: 0, TotalPackets: 1}, []byte("next"))

	if tracker.uniqueDetected != 1 {
		t.Fatalf("uniqueDetected got %d want 1", tracker.uniqueDetected)
	}
	if tracker.unrecovered != 1 {
		t.Fatalf("unrecovered got %d want 1", tracker.unrecovered)
	}
}
