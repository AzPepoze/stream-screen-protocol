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

	logger.Info("client", "joinLoop() STARTING, will send JOIN to %s every 1s", r.serverAddr.String())
	for {
		_ = r.conn.SetWriteDeadline(time.Time{})
		if _, err := r.conn.WriteToUDP(packet, r.serverAddr); err != nil {
			logger.Info("client", "joinLoop() JOIN write error: %v", err)
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
			atomic.AddUint64(&r.ccNACKRequestsSent, 1)
			atomic.AddUint64(&r.ccNACKPacketIDsSent, uint64(len(req.PacketIDs)))
		}
	}
}

// controlLoop sends queue state plus network delivery measurements.
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

			mediaReceived := atomic.SwapUint64(&r.ccMediaDataReceived, 0)
			_ = atomic.SwapUint64(&r.ccFECParityReceived, 0)
			uniqueMissing := atomic.SwapUint64(&r.ccUniqueMissingDetected, 0)
			_ = atomic.SwapUint64(&r.ccMissingRecoveredByNACK, 0)
			_ = atomic.SwapUint64(&r.ccMissingRecoveredByFEC, 0)
			unrecovered := atomic.SwapUint64(&r.ccMissingUnrecovered, 0)
			_ = atomic.SwapUint64(&r.ccNACKRequestsSent, 0)
			nackIDs := atomic.SwapUint64(&r.ccNACKPacketIDsSent, 0)
			_ = atomic.SwapUint64(&r.ccPacketsReceived, 0)
			bytesReceived := atomic.SwapUint64(&r.ccBytesReceived, 0)

			expectedMedia := mediaReceived + uniqueMissing
			var rawLossPermille uint16
			if expectedMedia > 0 {
				rawLoss := (uniqueMissing * 1000) / expectedMedia
				if rawLoss > 1000 {
					rawLoss = 1000
				}
				rawLossPermille = uint16(rawLoss)
			}
			var residualLossPermille uint16
			if expectedMedia > 0 {
				resLoss := (unrecovered * 1000) / expectedMedia
				if resLoss > 1000 {
					resLoss = 1000
				}
				residualLossPermille = uint16(resLoss)
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
					NACKSent:          uint32(nackIDs),
				},
				RTTMS:                uint16(rtt),
				LossPermille:         rawLossPermille,
				DeliveryRateKbps:     uint32(deliveryKbps),
				ResidualLossPermille: residualLossPermille,
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
