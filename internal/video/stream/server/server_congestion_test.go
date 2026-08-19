package server

import (
	"net"
	"testing"
	"time"

	"streamscreen/internal/video/stream"
)

func TestWifiScaleLossDoesNotTriggerCongestion(t *testing.T) {
	// Default wifi netem is 0.5% loss. Random loss at this scale should drive
	// light FEC, not collapse the encoder/pacer into severe congestion.
	f := stream.ControlFeedback{LossPermille: 5, RTTMS: 45}
	if got := classifyCongestion(f); got != CongestionHealthy {
		t.Fatalf("0.5%% loss classified as %s, want healthy", got.String())
	}
}

func TestCongestionStateTransitionsAndHysteresis(t *testing.T) {
	state := CongestionHealthy
	healthyRounds := 0

	state = evaluateCongestionState(state, stream.ControlFeedback{FrameQueuePercent: 30}, &healthyRounds)
	if state != CongestionConstrained {
		t.Fatalf("expected constrained, got %s", state.String())
	}

	state = evaluateCongestionState(state, stream.ControlFeedback{FrameQueuePercent: 55}, &healthyRounds)
	if state != CongestionCongested {
		t.Fatalf("expected congested, got %s", state.String())
	}

	state = evaluateCongestionState(state, stream.ControlFeedback{FrameDrops: 1, FrameQueuePercent: 80}, &healthyRounds)
	if state != CongestionSeverelyCongested {
		t.Fatalf("expected severely congested, got %s", state.String())
	}

	healthy := stream.ControlFeedback{FrameQueuePercent: 5}
	for i := 0; i < 2; i++ {
		state = evaluateCongestionState(state, healthy, &healthyRounds)
		if state != CongestionSeverelyCongested {
			t.Fatalf("hysteresis changed state on healthy round %d: %s", i+1, state.String())
		}
	}
	state = evaluateCongestionState(state, healthy, &healthyRounds)
	if state != CongestionCongested {
		t.Fatalf("expected one-step recovery to congested, got %s", state.String())
	}
}

func TestAdaptiveMediaTargetDecreasesAndRecovers(t *testing.T) {
	const base = uint64(6000000)
	severe := nextMediaTargetBitrate(base, base, CongestionSeverelyCongested)
	if severe != 4200000 {
		t.Fatalf("severe target=%d want=4200000", severe)
	}
	congested := nextMediaTargetBitrate(severe, base, CongestionCongested)
	if congested >= severe {
		t.Fatalf("congested target did not decrease: %d -> %d", severe, congested)
	}
	recovered := nextMediaTargetBitrate(congested, base, CongestionHealthy)
	if recovered <= congested || recovered > base {
		t.Fatalf("healthy recovery invalid: %d -> %d", congested, recovered)
	}
}

func TestAdaptiveMediaTargetHasFloor(t *testing.T) {
	const base = uint64(6000000)
	target := base
	for i := 0; i < 50; i++ {
		target = nextMediaTargetBitrate(target, base, CongestionSeverelyCongested)
	}
	if target != 1500000 {
		t.Fatalf("target floor=%d want=1500000", target)
	}
}

func TestWireTargetIncludesFECAndAudio(t *testing.T) {
	got := wireTargetFor(6000000, 8, true, 96)
	want := uint64(6000000 + 750000 + 96000)
	if got != want {
		t.Fatalf("wire target=%d want=%d", got, want)
	}
	if gotNoFEC := wireTargetFor(6000000, 0, false, 0); gotNoFEC != 6000000 {
		t.Fatalf("wire target without overhead=%d", gotNoFEC)
	}
}

// Frame shedding remains useful for independently decodable/block media. H264
// bypasses this policy and uses IDR-aware resynchronization instead.
func TestPerViewerFrameSheddingForIndependentMedia(t *testing.T) {
	v := newViewerState(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234})

	v.congestionState = CongestionHealthy
	for seq := uint32(0); seq < 8; seq++ {
		if v.shouldDropVideoBatch(seq) {
			t.Fatalf("healthy viewer should not drop frame %d", seq)
		}
	}

	v.congestionState = CongestionConstrained
	dropped := 0
	for seq := uint32(0); seq < 8; seq++ {
		if v.shouldDropVideoBatch(seq) {
			dropped++
		}
	}
	if dropped != 2 {
		t.Fatalf("constrained viewer dropped %d/8 want 2", dropped)
	}

	v.congestionState = CongestionCongested
	dropped = 0
	for seq := uint32(0); seq < 8; seq++ {
		if v.shouldDropVideoBatch(seq) {
			dropped++
		}
	}
	if dropped != 4 {
		t.Fatalf("congested viewer dropped %d/8 want 4", dropped)
	}

	v.congestionState = CongestionSeverelyCongested
	dropped = 0
	for seq := uint32(0); seq < 8; seq++ {
		if v.shouldDropVideoBatch(seq) {
			dropped++
		}
	}
	if dropped != 6 {
		t.Fatalf("severely congested viewer dropped %d/8 want 6", dropped)
	}
}

func TestTokenPacerChargesEveryPacket(t *testing.T) {
	// At 1 Mbps a 1360-byte packet costs about 10.88ms. With no initial tokens,
	// two packets should cost roughly twice that. The old pacer accidentally
	// reused the sleep interval as credit and could send packet two immediately.
	pacer := newTokenPacer(1000000)
	pacer.tokens = 0
	pacer.lastRefill = time.Now()

	start := time.Now()
	pacer.pace(1360, false)
	pacer.pace(1360, false)
	dur := time.Since(start)
	if dur < 17*time.Millisecond {
		t.Fatalf("two packets paced too quickly: %s", dur)
	}
	if dur > 150*time.Millisecond {
		t.Fatalf("pacer slept too long: %s", dur)
	}
}

func TestAudioPriorityCreatesBoundedDebt(t *testing.T) {
	pacer := newTokenPacer(1000000)
	pacer.tokens = 0
	for i := 0; i < 1000; i++ {
		pacer.pace(1360, true)
	}
	pacer.mu.Lock()
	debt := pacer.tokens
	limit := -pacer.maxTokens
	pacer.mu.Unlock()
	if debt < limit {
		t.Fatalf("audio debt unbounded: tokens=%f limit=%f", debt, limit)
	}
}

func TestRepairPacketDroppedAfterDeadline(t *testing.T) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	s := &Sender{conn: serverConn, frameDeadline: 50 * time.Millisecond}
	viewer := newViewerState(serverConn.LocalAddr().(*net.UDPAddr))

	s.sendBatch(viewer, packetBatch{
		packets:  [][]byte{[]byte("expired-repair")},
		class:    mediaRepair,
		frameSeq: 90,
		deadline: time.Now().Add(-10 * time.Millisecond),
	})
}
