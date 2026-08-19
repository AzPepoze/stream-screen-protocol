package server

import (
	"net"
	"time"

	"streamscreen/internal/logger"
	"streamscreen/internal/video/stream"
)

func (s *Sender) applyControlFeedback(addr *net.UDPAddr, f stream.ExtendedControlFeedback) {
	viewer := s.touchViewer(addr)
	if viewer == nil {
		return
	}

	pressure := int(f.FrameQueuePercent)
	if int(f.AudioQueuePercent) > pressure {
		pressure = int(f.AudioQueuePercent)
	}
	if f.FrameDrops > 0 {
		pressure += 20
	}
	if f.AudioDrops > 0 {
		pressure += 20
	}
	if f.NACKSent > 0 {
		pressure += 8
	}

	// Loss is reported in permille. Increase pressure gradually so a brief
	// random loss spike does not immediately collapse the stream, but sustained
	// multi-percent loss has a material effect on pacing.
	pressure += int(f.LossPermille) / 5
	if f.RTTMS > 80 {
		pressure += int(f.RTTMS-80) / 10
	}
	if f.JitterMS > 10 {
		pressure += int(f.JitterMS-10) / 2
	}
	if pressure > 100 {
		pressure = 100
	}

	videoGap := pressureToGap(pressure)
	// Audio remains deliberately less throttled than video.
	audioGap := pressureToGap(pressure / 3)
	viewer.setPacing(videoGap, audioGap)

	// XOR parity is enabled only on the lossy viewer path. Smaller groups mean
	// more redundancy: 1/16=6.25%, 1/8=12.5%, 1/4=25% before the small header.
	fecGroupSize := fecGroupForLoss(f.LossPermille)
	viewer.setFECGroupSize(fecGroupSize)

	if pressure >= 20 || f.FrameDrops > 0 || f.AudioDrops > 0 || f.NACKSent > 0 || fecGroupSize > 0 {
		logger.Info("server", "viewer=%s cc pressure=%d frame_q=%d%% audio_q=%d%% loss=%.1f%% rtt=%dms jitter=%dms rate=%dkbps -> video_gap=%s audio_gap=%s fec_group=%d",
			addr.String(), pressure, f.FrameQueuePercent, f.AudioQueuePercent,
			float64(f.LossPermille)/10.0, f.RTTMS, f.JitterMS, f.DeliveryRateKbps,
			videoGap, audioGap, fecGroupSize)
	}
}

func fecGroupForLoss(lossPermille uint16) int {
	switch {
	case lossPermille < 5: // < 0.5%
		return 0
	case lossPermille < 20: // < 2%
		return 16
	case lossPermille < 50: // < 5%
		return 8
	default:
		return 4
	}
}

func pressureToGap(pressure int) time.Duration {
	switch {
	case pressure < 30:
		return 0
	case pressure < 50:
		return 10 * time.Microsecond
	case pressure < 70:
		return 25 * time.Microsecond
	case pressure < 85:
		return 50 * time.Microsecond
	default:
		return 100 * time.Microsecond
	}
}
