package server

import (
	"net"

	"streamscreen/internal/logger"
	"streamscreen/internal/video/stream"
)

func (s *Sender) applyControlFeedback(addr *net.UDPAddr, f stream.ControlFeedback) {
	viewer := s.touchViewer(addr)
	if viewer == nil {
		return
	}

	viewer.mu.Lock()
	oldState := viewer.congestionState
	newState := evaluateCongestionState(oldState, f, &viewer.healthyRounds)
	viewer.congestionState = newState
	fecGroupSize := fecGroupForLoss(f.LossPermille, newState)
	viewer.fecGroupSize = fecGroupSize

	s.cfgMu.RLock()
	baseBitrateKbps := 6000
	if s.codecName == "h264" {
		if br, ok := s.cfg.Capture.H264CodecConfig["bitrate"]; ok {
			if v, ok := br.(int); ok && v > 0 {
				baseBitrateKbps = v
			}
		}
	}
	s.cfgMu.RUnlock()

	baseBitrateBps := uint64(baseBitrateKbps) * 1000
	var targetBps uint64
	switch newState {
	case CongestionHealthy:
		targetBps = baseBitrateBps
	case CongestionConstrained:
		targetBps = uint64(float64(baseBitrateBps) * 0.8)
	case CongestionCongested:
		targetBps = uint64(float64(baseBitrateBps) * 0.5)
	case CongestionSeverelyCongested:
		targetBps = uint64(float64(baseBitrateBps) * 0.3)
	}
	if targetBps < 500000 {
		targetBps = 500000
	}
	if f.DeliveryRateKbps > 0 {
		deliveryBps := uint64(f.DeliveryRateKbps) * 1000
		if deliveryBps < targetBps && deliveryBps >= 500000 {
			targetBps = deliveryBps
		}
	}
	viewer.pacer.setTargetBitrate(targetBps)
	viewer.mu.Unlock()

	if newState != CongestionHealthy || f.FrameDrops > 0 || f.AudioDrops > 0 || f.NACKSent > 0 || fecGroupSize > 0 {
		logger.Info("server", "viewer=%s cc state=%s target=%dkbps frame_q=%d%% audio_q=%d%% raw_loss=%.1f%% res_loss=%.1f%% rtt=%dms jitter=%dms rate=%dkbps fec_group=%d",
			addr.String(), newState.String(), targetBps/1000, f.FrameQueuePercent, f.AudioQueuePercent,
			float64(f.LossPermille)/10.0, float64(f.ResidualLossPermille)/10.0, f.RTTMS, f.JitterMS, f.DeliveryRateKbps,
			fecGroupSize)
	}
}

func evaluateCongestionState(current CongestionState, f stream.ControlFeedback, healthyRounds *int) CongestionState {
	// Fast escalation to protect queue and network
	if f.FrameDrops > 0 || f.FrameQueuePercent >= 75 || f.LossPermille >= 150 {
		*healthyRounds = 0
		return CongestionSeverelyCongested
	}
	if f.FrameQueuePercent >= 50 || f.LossPermille >= 60 || f.ResidualLossPermille >= 30 || f.RTTMS > 150 {
		*healthyRounds = 0
		if current < CongestionCongested {
			return CongestionCongested
		}
		return current
	}
	if f.FrameQueuePercent >= 25 || f.LossPermille >= 20 || f.RTTMS > 100 {
		*healthyRounds = 0
		if current < CongestionConstrained {
			return CongestionConstrained
		}
		return current
	}

	// Healthy observations: recover gradually with hysteresis
	*healthyRounds++
	if *healthyRounds >= 3 {
		*healthyRounds = 0
		if current > CongestionHealthy {
			return current - 1
		}
	}
	return current
}

func fecGroupForLoss(lossPermille uint16, state CongestionState) int {
	if state == CongestionSeverelyCongested {
		// When severely congested, adding 25% FEC overhead worsens the bottleneck.
		if lossPermille >= 50 {
			return 8 // cap at 12.5% overhead
		}
		return 0
	}
	switch {
	case lossPermille < 5: // < 0.5%
		return 0
	case lossPermille < 20: // < 2%
		return 16
	case lossPermille < 50: // < 5%
		return 8
	default:
		if state == CongestionCongested {
			return 8
		}
		return 4
	}
}
