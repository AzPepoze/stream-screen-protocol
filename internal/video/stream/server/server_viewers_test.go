package server

import (
	"context"
	"net"
	"testing"
	"time"

	"streamscreen/internal/video/stream"
)

func TestBroadcastVideoBatchFansOutToTwoViewers(t *testing.T) {
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	clientA, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Close()
	clientB, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer clientB.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Sender{
		conn:          serverConn,
		ctx:           ctx,
		cancel:        cancel,
		viewers:       make(map[string]*viewerState),
		clientTimeout: time.Second,
		frameDeadline: 100 * time.Millisecond,
	}
	defer s.stopAllViewers()

	s.registerViewer(clientA.LocalAddr().(*net.UDPAddr))
	s.registerViewer(clientB.LocalAddr().(*net.UDPAddr))
	if got := s.viewerCount(); got != 2 {
		t.Fatalf("viewer count=%d want=2", got)
	}

	want := []byte("immutable-csp-packet")
	s.broadcastVideoBatch([][]byte{want}, 1)

	for name, conn := range map[string]*net.UDPConn{"a": clientA, "b": clientB} {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 128)
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("client %s did not receive fanout: %v", name, err)
		}
		if string(buf[:n]) != string(want) {
			t.Fatalf("client %s got %q want %q", name, buf[:n], want)
		}
	}
}

func TestH264DoesNotRandomlyShedDependentFrames(t *testing.T) {
	viewer := newViewerState(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 20000})
	viewer.congestionState = CongestionSeverelyCongested

	s := &Sender{
		viewers:       map[string]*viewerState{viewer.addr.String(): viewer},
		frameDeadline: 100 * time.Millisecond,
		clientTimeout: time.Second,
		codecName:     "h264",
	}

	// A severe congestion state used to discard 75% of H264 AUs by frameSeq
	// modulo. The H264 path must enqueue this P-frame while synchronized.
	s.broadcastH264Batch([][]byte{[]byte("p-frame")}, 3, false)
	if got := len(viewer.videoQ); got != 1 {
		t.Fatalf("synchronized H264 viewer queue=%d want=1", got)
	}
}

func TestH264ResyncWaitsForIDR(t *testing.T) {
	viewer := newViewerState(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 20001})
	viewer.needsKeyframe = true
	s := &Sender{
		viewers:       map[string]*viewerState{viewer.addr.String(): viewer},
		frameDeadline: 100 * time.Millisecond,
		clientTimeout: time.Second,
		codecName:     "h264",
	}

	s.broadcastH264Batch([][]byte{[]byte("dependent")}, 10, false)
	if got := len(viewer.videoQ); got != 0 {
		t.Fatalf("dependent frame enqueued during resync: %d", got)
	}

	s.broadcastH264Batch([][]byte{[]byte("idr")}, 11, true)
	if got := len(viewer.videoQ); got != 1 {
		t.Fatalf("IDR not enqueued during resync: %d", got)
	}
	viewer.mu.RLock()
	needs := viewer.needsKeyframe
	viewer.mu.RUnlock()
	if needs {
		t.Fatal("viewer remained in keyframe-wait state after IDR enqueue")
	}
}

func TestH264AccessUnitIDRDetection(t *testing.T) {
	idr := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 1, 0x65, 9, 9}
	if !h264AccessUnitHasIDR(idr) {
		t.Fatal("failed to detect Annex-B IDR NAL")
	}
	pframe := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 1, 0x41, 9, 9}
	if h264AccessUnitHasIDR(pframe) {
		t.Fatal("P-frame access unit incorrectly detected as IDR")
	}
}

func TestFECGroupForLoss(t *testing.T) {
	cases := []struct {
		loss  uint16
		state CongestionState
		want  int
	}{
		{0, CongestionHealthy, 0},
		{4, CongestionHealthy, 0},
		{5, CongestionHealthy, 16},
		{19, CongestionHealthy, 16},
		{20, CongestionHealthy, 8},
		{49, CongestionHealthy, 8},
		{50, CongestionHealthy, 8},
		{100, CongestionHealthy, 8},
		{100, CongestionCongested, 8},
		{100, CongestionSeverelyCongested, 8},
		{40, CongestionSeverelyCongested, 0},
	}
	for _, tc := range cases {
		if got := fecGroupForLoss(tc.loss, tc.state); got != tc.want {
			t.Fatalf("loss=%d state=%s permille fec_group=%d want=%d", tc.loss, tc.state.String(), got, tc.want)
		}
	}
}

func TestPacketBeforeDeadline(t *testing.T) {
	s := &Sender{frameDeadline: 50 * time.Millisecond}
	fresh := make([]byte, stream.CSPHeaderSize)
	freshHeader := stream.PacketHeader{Version: stream.CSPVersion, PacketType: stream.CSPPacketTypeData, Timestamp: stream.NowTimestampMS()}
	freshHeader.Marshal(fresh)
	if !s.packetBeforeDeadline(fresh) {
		t.Fatal("fresh packet should be retransmittable")
	}

	stale := make([]byte, stream.CSPHeaderSize)
	staleHeader := stream.PacketHeader{Version: stream.CSPVersion, PacketType: stream.CSPPacketTypeData, Timestamp: stream.NowTimestampMS() - 500}
	staleHeader.Marshal(stale)
	if s.packetBeforeDeadline(stale) {
		t.Fatal("stale packet should not be retransmitted")
	}
}

func TestFrameDeadlineForFPS(t *testing.T) {
	if got := frameDeadlineForFPS(120); got < 30*time.Millisecond || got > 40*time.Millisecond {
		t.Fatalf("120fps deadline=%s", got)
	}
	if got := frameDeadlineForFPS(240); got != 25*time.Millisecond {
		t.Fatalf("240fps deadline=%s want=25ms", got)
	}
}
