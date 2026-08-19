package client

import (
	"sync/atomic"
	"time"

	"streamscreen/internal/logger"
	"streamscreen/internal/video/stream"
)

// joinLoop sends JOIN packets to server every 1 second.
func (r *ClientReceiver) joinLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	packet := stream.MarshalJoin("")

	logger.Info("Client: joinLoop() STARTING, will send JOIN to %s every 1s", r.serverAddr.String())
	for {
		_ = r.conn.SetWriteDeadline(time.Time{})
		if _, err := r.conn.WriteToUDP(packet, r.serverAddr); err != nil {
			logger.Info("Client: joinLoop() JOIN write error: %v", err)
		}
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *ClientReceiver) nackLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case req := <-r.jitterBuffer.nackChan:
			packet := stream.MarshalNACK(req.FrameSeq, req.PacketIDs)
			_, _ = r.conn.WriteToUDP(packet, r.serverAddr)
			atomic.AddUint64(&r.ccNACKSent, uint64(len(req.PacketIDs)))
		}
	}
}

// controlLoop sends queue state plus network delivery measurements. A small
// echo probe gives the sender an actual RTT instead of inferring latency from
// queue pressure alone.
func (r *ClientReceiver) controlLoop() {
	const interval = 500 * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			nonce := atomic.AddUint32(&r.ccProbeNonce, 1)
			_, _ = r.conn.WriteToUDP(stream.MarshalProbe(nonce, stream.NowTimestampMS()), r.serverAddr)

			received := atomic.SwapUint64(&r.ccPacketsReceived, 0)
			bytesReceived := atomic.SwapUint64(&r.ccBytesReceived, 0)
			missing := atomic.SwapUint64(&r.ccNACKSent, 0)
			fecRecovered := atomic.SwapUint64(&r.ccFECRecovered, 0)
			observedLoss := missing + fecRecovered

			var lossPermille uint16
			if total := received + observedLoss; total > 0 {
				loss := (observedLoss * 1000) / total
				if loss > 1000 {
					loss = 1000
				}
				lossPermille = uint16(loss)
			}
			deliveryKbps := (bytesReceived * 8 * uint64(time.Second/interval)) / 1000
			if deliveryKbps > uint64(^uint32(0)) {
				deliveryKbps = uint64(^uint32(0))
			}
			rtt := atomic.LoadUint32(&r.ccRTTMS)
			if rtt > uint32(^uint16(0)) {
				rtt = uint32(^uint16(0))
			}

			feedback := stream.ExtendedControlFeedback{
				ControlFeedback: stream.ControlFeedback{
					FrameQueuePercent: queuePercent(len(r.frameChan), cap(r.frameChan)),
					AudioQueuePercent: queuePercent(len(r.audioFrames), cap(r.audioFrames)),
					FrameDrops:        uint32(atomic.SwapUint64(&r.ccFrameDrops, 0)),
					AudioDrops:        uint32(atomic.SwapUint64(&r.ccAudioDrops, 0)),
					NACKSent:          uint32(missing),
				},
				RTTMS:            uint16(rtt),
				LossPermille:     lossPermille,
				DeliveryRateKbps: uint32(deliveryKbps),
			}
			_, _ = r.conn.WriteToUDP(stream.MarshalExtendedControlFeedback(feedback), r.serverAddr)
		}
	}
}

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
