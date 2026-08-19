package server

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"streamscreen/internal/video/stream"
)

type mediaClass uint8

const (
	mediaVideo mediaClass = iota + 1
	mediaAudio
	mediaRepair
)

type packetBatch struct {
	packets  [][]byte
	class    mediaClass
	frameSeq uint32
	deadline time.Time
}

type viewerState struct {
	addr *net.UDPAddr

	mu           sync.RWMutex
	lastSeen     time.Time
	videoGap     time.Duration
	audioGap     time.Duration
	fecGroupSize int

	videoQ  chan packetBatch
	audioQ  chan packetBatch
	repairQ chan packetBatch
	done    chan struct{}
	once    sync.Once

	droppedVideo uint64
}

func newViewerState(addr *net.UDPAddr) *viewerState {
	copyAddr := *addr
	return &viewerState{
		addr:     &copyAddr,
		lastSeen: time.Now(),
		videoQ:   make(chan packetBatch, 3),
		audioQ:   make(chan packetBatch, 32),
		repairQ:  make(chan packetBatch, 16),
		done:     make(chan struct{}),
	}
}

func (v *viewerState) touch() {
	v.mu.Lock()
	v.lastSeen = time.Now()
	v.mu.Unlock()
}

func (v *viewerState) seenAt() time.Time {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastSeen
}

func (v *viewerState) setPacing(videoGap, audioGap time.Duration) {
	v.mu.Lock()
	v.videoGap = videoGap
	v.audioGap = audioGap
	v.mu.Unlock()
}

func (v *viewerState) setFECGroupSize(groupSize int) {
	v.mu.Lock()
	v.fecGroupSize = groupSize
	v.mu.Unlock()
}

func (v *viewerState) fecGroup() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.fecGroupSize
}

func (v *viewerState) pacing(class mediaClass) time.Duration {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if class == mediaAudio {
		return v.audioGap
	}
	return v.videoGap
}

func (v *viewerState) stop() {
	v.once.Do(func() { close(v.done) })
}

func (s *Sender) registerViewer(addr *net.UDPAddr) *viewerState {
	if addr == nil {
		return nil
	}
	key := addr.String()
	s.viewersMu.Lock()
	if existing := s.viewers[key]; existing != nil {
		existing.touch()
		s.viewersMu.Unlock()
		return existing
	}
	viewer := newViewerState(addr)
	s.viewers[key] = viewer
	s.viewersMu.Unlock()
	go s.viewerSendLoop(viewer)
	return viewer
}

func (s *Sender) touchViewer(addr *net.UDPAddr) *viewerState {
	return s.registerViewer(addr)
}

func (s *Sender) viewerFor(addr *net.UDPAddr) *viewerState {
	if addr == nil {
		return nil
	}
	s.viewersMu.RLock()
	viewer := s.viewers[addr.String()]
	s.viewersMu.RUnlock()
	return viewer
}

func (s *Sender) activeViewers() []*viewerState {
	now := time.Now()
	s.viewersMu.Lock()
	viewers := make([]*viewerState, 0, len(s.viewers))
	for key, viewer := range s.viewers {
		if s.clientTimeout > 0 && now.Sub(viewer.seenAt()) > s.clientTimeout {
			delete(s.viewers, key)
			viewer.stop()
			continue
		}
		viewers = append(viewers, viewer)
	}
	s.viewersMu.Unlock()
	return viewers
}

func (s *Sender) viewerCount() int {
	return len(s.activeViewers())
}

func (s *Sender) stopAllViewers() {
	s.viewersMu.Lock()
	for key, viewer := range s.viewers {
		delete(s.viewers, key)
		viewer.stop()
	}
	s.viewersMu.Unlock()
}

func (s *Sender) viewerCleanupLoop() {
	interval := time.Second
	if s.clientTimeout > 0 && s.clientTimeout/2 < interval {
		interval = s.clientTimeout / 2
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			_ = s.activeViewers()
		}
	}
}

func enqueueLatest(ch chan packetBatch, batch packetBatch) {
	select {
	case ch <- batch:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- batch:
	default:
	}
}

// broadcastVideoBatch keeps the encoded DATA packets shared between viewers,
// while adding parity only for viewers whose measured loss warrants it.
func (s *Sender) broadcastVideoBatch(packets [][]byte, frameSeq uint32) {
	if len(packets) == 0 {
		return
	}
	deadline := time.Now().Add(s.frameDeadline)
	fecCache := make(map[int][][]byte)
	for _, viewer := range s.activeViewers() {
		viewerPackets := packets
		groupSize := viewer.fecGroup()
		if groupSize >= 2 {
			augmented, ok := fecCache[groupSize]
			if !ok {
				fecPackets, err := stream.BuildXORFEC(packets, groupSize)
				if err == nil && len(fecPackets) > 0 {
					augmented = make([][]byte, 0, len(packets)+len(fecPackets))
					augmented = append(augmented, packets...)
					augmented = append(augmented, fecPackets...)
				} else {
					augmented = packets
				}
				fecCache[groupSize] = augmented
			}
			viewerPackets = augmented
		}

		batch := packetBatch{packets: viewerPackets, class: mediaVideo, frameSeq: frameSeq, deadline: deadline}
		before := len(viewer.videoQ)
		enqueueLatest(viewer.videoQ, batch)
		if before == cap(viewer.videoQ) {
			atomic.AddUint64(&viewer.droppedVideo, 1)
		}
	}
}

func (s *Sender) sendVideoBatchTo(addr *net.UDPAddr, packets [][]byte, frameSeq uint32) {
	viewer := s.touchViewer(addr)
	if viewer == nil || len(packets) == 0 {
		return
	}
	enqueueLatest(viewer.videoQ, packetBatch{
		packets:  packets,
		class:    mediaVideo,
		frameSeq: frameSeq,
		deadline: time.Now().Add(s.frameDeadline),
	})
}

func (s *Sender) broadcastAudioBatch(packets [][]byte, frameSeq uint32) {
	if len(packets) == 0 {
		return
	}
	batch := packetBatch{
		packets:  packets,
		class:    mediaAudio,
		frameSeq: frameSeq,
		deadline: time.Now().Add(150 * time.Millisecond),
	}
	for _, viewer := range s.activeViewers() {
		enqueueLatest(viewer.audioQ, batch)
	}
}

func (s *Sender) enqueueRepair(addr *net.UDPAddr, packets [][]byte, frameSeq uint32) {
	viewer := s.touchViewer(addr)
	if viewer == nil || len(packets) == 0 {
		return
	}
	enqueueLatest(viewer.repairQ, packetBatch{
		packets:  packets,
		class:    mediaRepair,
		frameSeq: frameSeq,
		deadline: time.Now().Add(s.frameDeadline),
	})
}

func (s *Sender) viewerSendLoop(viewer *viewerState) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-viewer.done:
			return
		default:
		}

		select {
		case batch := <-viewer.repairQ:
			s.sendBatch(viewer, batch)
			continue
		default:
		}
		select {
		case batch := <-viewer.audioQ:
			s.sendBatch(viewer, batch)
			continue
		default:
		}

		select {
		case <-s.ctx.Done():
			return
		case <-viewer.done:
			return
		case batch := <-viewer.repairQ:
			s.sendBatch(viewer, batch)
		case batch := <-viewer.audioQ:
			s.sendBatch(viewer, batch)
		case batch := <-viewer.videoQ:
			s.sendBatch(viewer, batch)
		}
	}
}

func (s *Sender) sendBatch(viewer *viewerState, batch packetBatch) {
	if !batch.deadline.IsZero() && time.Now().After(batch.deadline) {
		return
	}
	gap := viewer.pacing(batch.class)
	for _, packet := range batch.packets {
		if !batch.deadline.IsZero() && time.Now().After(batch.deadline) {
			return
		}
		_, _ = s.conn.WriteToUDP(packet, viewer.addr)
		if gap > 0 {
			time.Sleep(gap)
		}
	}
}
