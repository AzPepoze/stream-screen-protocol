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

func TestPacketBeforeDeadline(t *testing.T) {
	s := &Sender{frameDeadline: 50 * time.Millisecond}
	fresh := make([]byte, stream.CSPHeaderSize)
	stream.PacketHeader{Version: stream.CSPVersion, PacketType: stream.CSPPacketTypeData, Timestamp: stream.NowTimestampMS()}.Marshal(fresh)
	if !s.packetBeforeDeadline(fresh) {
		t.Fatal("fresh packet should be retransmittable")
	}

	stale := make([]byte, stream.CSPHeaderSize)
	stream.PacketHeader{Version: stream.CSPVersion, PacketType: stream.CSPPacketTypeData, Timestamp: stream.NowTimestampMS() - 500}.Marshal(stale)
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
