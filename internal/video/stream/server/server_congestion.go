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

	baseBitrateKbps, audioEnabled, audioBitrateKbps := s.transportBitrateConfig()
	baseBitrateBps := uint64(baseBitrateKbps) * 1000
	observed := classifyCongestion(f)

	viewer.mu.Lock()
	oldState := viewer.congestionState
	newState := evaluateCongestionState(oldState, f, &viewer.healthyRounds)
	viewer.congestionState = newState
	fecGroupSize := fecGroupForLoss(f.LossPermille, observed)
	viewer.fecGroupSize = fecGroupSize

	currentTarget := viewer.targetMediaBitrateBps
	if currentTarget == 0 || currentTarget > baseBitrateBps {
		currentTarget = baseBitrateBps
	}
	mediaTargetBps := nextMediaTargetBitrate(currentTarget, baseBitrateBps, observed)
	viewer.targetMediaBitrateBps = mediaTargetBps
	viewer.mu.Unlock()

	wireTargetBps := wireTargetFor(mediaTargetBps, fecGroupSize, audioEnabled, audioBitrateKbps)
	viewer.pacer.setTargetBitrate(wireTargetBps)

	// H.264 is a single shared reference stream. Arbitrary per-viewer frame
	// shedding is not codec-safe, so adapt the shared encoder to the lowest
	// active media target. A future simulcast/SVC layer can isolate qualities.
	s.updateAdaptiveH264Bitrate(baseBitrateKbps)

	if newState != CongestionHealthy || f.FrameDrops > 0 || f.AudioDrops > 0 || f.NACKSent > 0 || fecGroupSize > 0 {
		logger.Info("server", "viewer=%s cc state=%s media_target=%dkbps wire_target=%dkbps frame_q=%d%% audio_q=%d%% raw_loss=%.1f%% res_loss=%.1f%% rtt=%dms jitter=%dms delivery=%dkbps fec_group=%d",
			addr.String(), newState.String(), mediaTargetBps/1000, wireTargetBps/1000,
			f.FrameQueuePercent, f.AudioQueuePercent,
			float64(f.LossPermille)/10.0, float64(f.ResidualLossPermille)/10.0,
			f.RTTMS, f.JitterMS, f.DeliveryRateKbps, fecGroupSize)
	}
}

func (s *Sender) transportBitrateConfig() (videoKbps int, audioEnabled bool, audioKbps int) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	videoKbps = 6000
	if s.codecName == "h264" {
		if br, ok := s.cfg.Capture.H264CodecConfig["bitrate"]; ok {
			switch v := br.(type) {
			case int:
				if v > 0 {
					videoKbps = v
				}
			case float64:
				if v > 0 {
					videoKbps = int(v)
				}
			}
		}
	}
	audioEnabled = s.cfg.Audio.Enabled
	audioKbps = s.cfg.Audio.BitrateKbps
	if audioKbps < 0 {
		audioKbps = 0
	}
	return
}

func classifyCongestion(f stream.ControlFeedback) CongestionState {
	// Raw loss is useful for FEC, but modest random loss is not automatically a
	// bandwidth bottleneck. Queue growth, residual loss and drops are stronger
	// evidence that the sender must reduce media production.
	if f.FrameDrops > 0 || f.FrameQueuePercent >= 75 || f.ResidualLossPermille >= 100 || f.LossPermille >= 200 {
		return CongestionSeverelyCongested
	}
	if f.FrameQueuePercent >= 50 || f.ResidualLossPermille >= 30 || f.LossPermille >= 100 || f.RTTMS > 200 {
		return CongestionCongested
	}
	if f.FrameQueuePercent >= 25 || f.ResidualLossPermille >= 10 || f.LossPermille >= 50 || f.RTTMS > 120 {
		return CongestionConstrained
	}
	return CongestionHealthy
}

func evaluateCongestionState(current CongestionState, f stream.ControlFeedback, healthyRounds *int) CongestionState {
	observed := classifyCongestion(f)
	if observed != CongestionHealthy {
		*healthyRounds = 0
		if observed > current {
			return observed
		}
		return current
	}

	// Healthy observations recover the state gradually to avoid oscillation.
	*healthyRounds++
	if *healthyRounds >= 3 {
		*healthyRounds = 0
		if current > CongestionHealthy {
			return current - 1
		}
	}
	return current
}

func nextMediaTargetBitrate(current, base uint64, observed CongestionState) uint64 {
	if base == 0 {
		base = 6000000
	}
	if current == 0 || current > base {
		current = base
	}
	minimum := base / 4
	if minimum < 500000 {
		minimum = 500000
	}

	var next uint64
	switch observed {
	case CongestionSeverelyCongested:
		next = current * 70 / 100
	case CongestionCongested:
		next = current * 82 / 100
	case CongestionConstrained:
		next = current * 92 / 100
	default:
		step := base / 20 // 5% additive recovery per healthy feedback interval.
		if step < 100000 {
			step = 100000
		}
		next = current + step
	}

	if next < minimum {
		next = minimum
	}
	if next > base {
		next = base
	}
	return next
}

func wireTargetFor(mediaBps uint64, fecGroupSize int, audioEnabled bool, audioBitrateKbps int) uint64 {
	wire := mediaBps
	if fecGroupSize >= 2 {
		// One parity packet protects each group; packet/header variance is small
		// enough that this is a useful pacing budget approximation.
		wire += mediaBps / uint64(fecGroupSize)
	}
	if audioEnabled && audioBitrateKbps > 0 {
		wire += uint64(audioBitrateKbps) * 1000
	}
	return wire
}

func (s *Sender) updateAdaptiveH264Bitrate(baseBitrateKbps int) {
	s.cfgMu.RLock()
	if s.codecName != "h264" || s.h264Pipeline == nil {
		s.cfgMu.RUnlock()
		return
	}
	pipeline := s.h264Pipeline
	s.cfgMu.RUnlock()

	desiredBps := uint64(baseBitrateKbps) * 1000
	for _, viewer := range s.activeViewers() {
		if target := viewer.mediaTarget(); target > 0 && target < desiredBps {
			desiredBps = target
		}
	}
	desiredKbps := int(desiredBps / 1000)
	if desiredKbps <= 0 {
		desiredKbps = baseBitrateKbps
	}
	if err := pipeline.SetBitrateKbps(desiredKbps); err != nil {
		logger.Info("server", "adaptive h264 bitrate update failed: %v", err)
	}
}

func fecGroupForLoss(lossPermille uint16, state CongestionState) int {
	if state == CongestionSeverelyCongested {
		// When severely congested, adding 25% FEC overhead worsens a bottleneck.
		if lossPermille >= 50 {
			return 8
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
		return 8 // cap parity overhead at 12.5% under lossy conditions
	}
}
