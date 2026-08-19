package server

import (
	"net"
	"testing"
	"time"

	"streamscreen/internal/video/stream"
)

func TestCongestionStateTransitionsAndHysteresis(t *testing.T) {
	state := CongestionHealthy
	healthyRounds := 0

	// 1. Mild queue occupancy -> Constrained
	f1 := stream.ExtendedControlFeedback{
		ControlFeedback: stream.ControlFeedback{FrameQueuePercent: 30},
		LossPermille:     10,
	}
	state = evaluateCongestionState(state, f1, &healthyRounds)
	if state != CongestionConstrained {
		t.Fatalf("expected Constrained state, got %s", state.String())
	}
	if healthyRounds != 0 {
		t.Fatalf("healthyRounds should be reset to 0, got %d", healthyRounds)
	}

	// 2. High loss & queue -> Congested
	f2 := stream.ExtendedControlFeedback{
		ControlFeedback: stream.ControlFeedback{FrameQueuePercent: 55},
		LossPermille:     65,
	}
	state = evaluateCongestionState(state, f2, &healthyRounds)
	if state != CongestionCongested {
		t.Fatalf("expected Congested state, got %s", state.String())
	}

	// 3. Drops -> SeverelyCongested
	f3 := stream.ExtendedControlFeedback{
		ControlFeedback: stream.ControlFeedback{FrameDrops: 1, FrameQueuePercent: 80},
		LossPermille:     160,
	}
	state = evaluateCongestionState(state, f3, &healthyRounds)
	if state != CongestionSeverelyCongested {
		t.Fatalf("expected SeverelyCongested state, got %s", state.String())
	}

	// 4. Healthy feedback: 1st round should NOT immediately jump to Healthy (hysteresis)
	fHealthy := stream.ExtendedControlFeedback{
		ControlFeedback: stream.ControlFeedback{FrameQueuePercent: 5, FrameDrops: 0},
		LossPermille:     0,
	}
	state = evaluateCongestionState(state, fHealthy, &healthyRounds)
	if state != CongestionSeverelyCongested {
		t.Fatalf("hysteresis failed: state changed prematurely on 1st round to %s", state.String())
	}

	// 2nd healthy round
	state = evaluateCongestionState(state, fHealthy, &healthyRounds)
	if state != CongestionSeverelyCongested {
		t.Fatalf("hysteresis failed: state changed prematurely on 2nd round to %s", state.String())
	}

	// 3rd healthy round -> steps down to Congested
	state = evaluateCongestionState(state, fHealthy, &healthyRounds)
	if state != CongestionCongested {
		t.Fatalf("expected step-down to Congested, got %s", state.String())
	}
}

func TestPerViewerFrameShedding(t *testing.T) {
	v := newViewerState(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234})

	// Healthy: drops 0% of frames
	v.congestionState = CongestionHealthy
	for seq := uint32(0); seq < 8; seq++ {
		if v.shouldDropVideoBatch(seq) {
			t.Fatalf("Healthy viewer should not drop frame %d", seq)
		}
	}

	// Constrained: drops 1 in 4 frames
	v.congestionState = CongestionConstrained
	dropped := 0
	for seq := uint32(0); seq < 8; seq++ {
		if v.shouldDropVideoBatch(seq) {
			dropped++
		}
	}
	if dropped != 2 {
		t.Fatalf("Constrained viewer should drop 2 of 8 frames, got %d", dropped)
	}

	// Congested: drops 1 in 2 frames (50%)
	v.congestionState = CongestionCongested
	dropped = 0
	for seq := uint32(0); seq < 8; seq++ {
		if v.shouldDropVideoBatch(seq) {
			dropped++
		}
	}
	if dropped != 4 {
		t.Fatalf("Congested viewer should drop 4 of 8 frames, got %d", dropped)
	}

	// SeverelyCongested: drops 3 in 4 frames (75%)
	v.congestionState = CongestionSeverelyCongested
	dropped = 0
	for seq := uint32(0); seq < 8; seq++ {
		if v.shouldDropVideoBatch(seq) {
			dropped++
		}
	}
	if dropped != 6 {
		t.Fatalf("SeverelyCongested viewer should drop 6 of 8 frames, got %d", dropped)
	}
}

func TestTokenPacerEnforcesBitrate(t *testing.T) {
	// Target rate: 10,000,000 bps (10 Mbps = 1.25 MB/s)
	pacer := newTokenPacer(10000000)
	pacer.tokens = 0
	pacer.lastRefill = time.Now()

	// Pacing a 1360-byte packet with 0 tokens should compute positive wait time without panic
	start := time.Now()
	pacer.pace(1360, false)
	dur := time.Since(start)

	// Expected wait ~ (1360 * 8 / 10,000,000) s = 1.088 ms
	if dur > 100*time.Millisecond {
		t.Fatalf("pacer slept too long: %s", dur)
	}

	// Immediate (audio) pacing should not block even when tokens are 0
	start = time.Now()
	pacer.pace(200, true)
	if durAudio := time.Since(start); durAudio > 5*time.Millisecond {
		t.Fatalf("immediate audio pacing blocked: %s", durAudio)
	}
}

func TestRepairPacketDroppedAfterDeadline(t *testing.T) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	s := &Sender{
		conn:          serverConn,
		frameDeadline: 50 * time.Millisecond,
	}

	freshBatch := packetBatch{
		packets:  [][]byte{[]byte("fresh-repair")},
		class:    mediaRepair,
		frameSeq: 100,
		deadline: time.Now().Add(50 * time.Millisecond),
	}
	expiredBatch := packetBatch{
		packets:  [][]byte{[]byte("expired-repair")},
		class:    mediaRepair,
		frameSeq: 90,
		deadline: time.Now().Add(-10 * time.Millisecond),
	}

	viewer := newViewerState(serverConn.LocalAddr().(*net.UDPAddr))

	// Send expired batch: should be dropped immediately without writing to UDP
	s.sendBatch(viewer, expiredBatch)

	// Fresh batch: valid
	s.sendBatch(viewer, freshBatch)
}
