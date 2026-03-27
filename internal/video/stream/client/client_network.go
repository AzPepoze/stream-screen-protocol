package client

import (
	"log"
	"streamscreen/internal/video/stream"
	"sync/atomic"
	"time"
)

// joinLoop sends JOIN packets to server every 1 second
func (r *ClientReceiver) joinLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	packet := stream.MarshalJoin("")

	log.Printf("Client: joinLoop() STARTING, will send JOIN to %s every 1s", r.serverAddr.String())
	for {
		_ = r.conn.SetWriteDeadline(time.Time{})
		if _, err := r.conn.WriteToUDP(packet, r.serverAddr); err != nil {
			log.Printf("Client: joinLoop() JOIN write error: %v", err)
		}

		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// nackLoop handles NACK requests from jitter buffer
func (r *ClientReceiver) nackLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case req := <-r.jitterBuffer.nackChan:
			packet := stream.MarshalNACK(req.FrameSeq, req.PacketIDs)
			_, _ = r.conn.WriteToUDP(packet, r.serverAddr)
			atomic.AddUint64(&r.ccNACKSent, 1)
		}
	}
}

// controlLoop sends periodic control feedback to server
func (r *ClientReceiver) controlLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			packet := stream.MarshalControlFeedback(stream.ControlFeedback{
				FrameQueuePercent: queuePercent(len(r.frameChan), cap(r.frameChan)),
				AudioQueuePercent: queuePercent(len(r.audioFrames), cap(r.audioFrames)),
				FrameDrops:        uint32(atomic.SwapUint64(&r.ccFrameDrops, 0)),
				AudioDrops:        uint32(atomic.SwapUint64(&r.ccAudioDrops, 0)),
				NACKSent:          uint32(atomic.SwapUint64(&r.ccNACKSent, 0)),
			})
			_, _ = r.conn.WriteToUDP(packet, r.serverAddr)
		}
	}
}

// queuePercent calculates queue utilization as percentage
func queuePercent(length, capacity int) uint8 {
	if capacity <= 0 {
		return 0
	}
	p := (length * 100) / capacity
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return uint8(p)
}
