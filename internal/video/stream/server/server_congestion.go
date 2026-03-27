package server

import (
	"log"
	"time"

	"streamscreen/internal/video/stream"
)

func (s *Sender) videoPacketGap() time.Duration {
	s.ccMu.RLock()
	defer s.ccMu.RUnlock()
	return s.ccVideoGap
}

func (s *Sender) audioPacketGap() time.Duration {
	s.ccMu.RLock()
	defer s.ccMu.RUnlock()
	return s.ccAudioGap
}

func (s *Sender) applyControlFeedback(f stream.ControlFeedback) {
	pressure := int(f.FrameQueuePercent)
	if int(f.AudioQueuePercent) > pressure {
		pressure = int(f.AudioQueuePercent)
	}
	if f.FrameDrops > 0 {
		pressure += 20
	}
	if f.AudioDrops > 0 {
		pressure += 15
	}
	if f.NACKSent > 0 {
		pressure += 10
	}
	if pressure > 100 {
		pressure = 100
	}

	videoGap := pressureToGap(pressure)
	audioGap := pressureToGap(pressure / 2)

	s.ccMu.Lock()
	s.ccVideoGap = videoGap
	s.ccAudioGap = audioGap
	now := time.Now()
	shouldLog := now.Sub(s.ccLastLogAt) >= 2*time.Second
	if shouldLog {
		s.ccLastLogAt = now
	}
	s.ccMu.Unlock()

	if shouldLog {
		log.Printf("[server] cc feedback: frame_q=%d%% audio_q=%d%% frame_drop=%d audio_drop=%d nacks=%d -> video_gap=%s audio_gap=%s",
			f.FrameQueuePercent, f.AudioQueuePercent, f.FrameDrops, f.AudioDrops, f.NACKSent, videoGap, audioGap)
	}
}

func pressureToGap(pressure int) time.Duration {
	switch {
	case pressure < 30:
		return 0
	case pressure < 50:
		return 50 * time.Microsecond
	case pressure < 70:
		return 120 * time.Microsecond
	case pressure < 85:
		return 250 * time.Microsecond
	default:
		return 400 * time.Microsecond
	}
}
