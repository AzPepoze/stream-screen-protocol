package server

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"streamscreen/internal/logger"
	"streamscreen/internal/video/stream"
)

type mediaClass uint8

const (
	mediaVideo mediaClass = iota + 1
	mediaAudio
	mediaRepair
)

type CongestionState int

const (
	CongestionHealthy CongestionState = iota
	CongestionConstrained
	CongestionCongested
	CongestionSeverelyCongested
)

func (c CongestionState) String() string {
	switch c {
	case CongestionHealthy:
		return "healthy"
	case CongestionConstrained:
		return "constrained"
	case CongestionCongested:
		return "congested"
	case CongestionSeverelyCongested:
		return "severely_congested"
	default:
		return "unknown"
	}
}

type tokenPacer struct {
	mu               sync.Mutex
	targetBitrateBps uint64
	tokens           float64
	maxTokens        float64
	lastRefill       time.Time
}

func burstBytesForRate(targetBps uint64) float64 {
	// Keep roughly 100ms of send credit so an occasional large IDR can burst
	// without being stretched across several frame deadlines. Bound it so an
	// idle sender cannot accumulate an unbounded UDP burst.
	burst := float64(targetBps) * 0.100 / 8.0
	if burst < 16*1024 {
		burst = 16 * 1024
	}
	if burst > 256*1024 {
		burst = 256 * 1024
	}
	return burst
}

func newTokenPacer(targetBps uint64) *tokenPacer {
	if targetBps == 0 {
		targetBps = 6000000
	}
	burst := burstBytesForRate(targetBps)
	return &tokenPacer{
		targetBitrateBps: targetBps,
		tokens:           burst,
		maxTokens:        burst,
		lastRefill:       time.Now(),
	}
}

func (p *tokenPacer) setTargetBitrate(bps uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if bps < 500000 {
		bps = 500000
	}
	if bps > 50000000 {
		bps = 50000000
	}
	p.targetBitrateBps = bps
	p.maxTokens = burstBytesForRate(bps)
	if p.tokens > p.maxTokens {
		p.tokens = p.maxTokens
	}
	if p.tokens < -p.maxTokens {
		p.tokens = -p.maxTokens
	}
}

func (p *tokenPacer) targetBitrate() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.targetBitrateBps
}

func (p *tokenPacer) pace(packetBytes int, immediate bool) {
	if packetBytes <= 0 {
		return
	}
	cost := float64(packetBytes)

	for {
		p.mu.Lock()
		now := time.Now()
		if p.lastRefill.IsZero() {
			p.lastRefill = now
			p.tokens = p.maxTokens
		}
		elapsed := now.Sub(p.lastRefill).Seconds()
		p.lastRefill = now
		p.tokens += float64(p.targetBitrateBps) * elapsed / 8.0
		if p.tokens > p.maxTokens {
			p.tokens = p.maxTokens
		}

		if immediate {
			// Audio has scheduling priority, not free bandwidth. Allow a bounded
			// debt so a short audio burst is immediate but later media yields.
			p.tokens -= cost
			if p.tokens < -p.maxTokens {
				p.tokens = -p.maxTokens
			}
			p.mu.Unlock()
			return
		}

		if p.tokens >= cost {
			p.tokens -= cost
			p.mu.Unlock()
			return
		}

		needed := cost - p.tokens
		rate := p.targetBitrateBps
		p.mu.Unlock()

		if rate == 0 {
			rate = 500000
		}
		wait := time.Duration((needed * 8.0 / float64(rate)) * float64(time.Second))
		if wait <= 0 {
			continue
		}
		// Wake periodically so a control update can raise the target quickly.
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond
		}
		time.Sleep(wait)
	}
}

type packetBatch struct {
	packets  [][]byte
	class    mediaClass
	frameSeq uint32
	deadline time.Time
	keyframe bool
}

type viewerState struct {
	addr *net.UDPAddr

	mu                    sync.RWMutex
	lastSeen              time.Time
	fecGroupSize          int
	congestionState       CongestionState
	healthyRounds         int
	targetMediaBitrateBps uint64
	needsKeyframe         bool
	pacer                 *tokenPacer

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
		addr:                    &copyAddr,
		lastSeen:                time.Now(),
		targetMediaBitrateBps: 6000000,
		pacer:                   newTokenPacer(6000000),
		videoQ:                  make(chan packetBatch, 3),
		audioQ:                  make(chan packetBatch, 32),
		repairQ:                 make(chan packetBatch, 16),
		done:                    make(chan struct{}),
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

func (v *viewerState) mediaTarget() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.targetMediaBitrateBps
}

func (v *viewerState) shouldDropVideoBatch(frameSeq uint32) bool {
	v.mu.RLock()
	state := v.congestionState
	v.mu.RUnlock()

	// This policy is safe for independently decodable/block-based media only.
	// H.264 uses broadcastH264Batch, which preserves the shared reference chain.
	switch state {
	case CongestionHealthy:
		return false
	case CongestionConstrained:
		return (frameSeq % 4) == 0
	case CongestionCongested:
		return (frameSeq % 2) == 0
	case CongestionSeverelyCongested:
		return (frameSeq % 4) != 0
	default:
		return false
	}
}

func (v *viewerState) stop() {
	v.once.Do(func() { close(v.done) })
}

func (s *Sender) registerViewer(addr *net.UDPAddr) (*viewerState, bool) {
	if addr == nil {
		return nil, false
	}
	key := addr.String()
	s.viewersMu.Lock()
	if existing := s.viewers[key]; existing != nil {
		existing.touch()
		s.viewersMu.Unlock()
		return existing, false
	}
	viewer := newViewerState(addr)
	s.viewers[key] = viewer
	s.viewersMu.Unlock()
	go s.viewerSendLoop(viewer)
	return viewer, true
}

func (s *Sender) touchViewer(addr *net.UDPAddr) *viewerState {
	v, _ := s.registerViewer(addr)
	return v
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
			logger.Info("server", "viewer expired %s (inactive for %s)", key, now.Sub(viewer.seenAt()).Round(time.Millisecond))
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

func (v *viewerState) flushVideo() {
	for {
		select {
		case <-v.videoQ:
		default:
			return
		}
	}
}

func (s *Sender) markViewerNeedsKeyframe(viewer *viewerState) {
	if viewer == nil {
		return
	}
	viewer.mu.Lock()
	already := viewer.needsKeyframe
	viewer.needsKeyframe = true
	viewer.mu.Unlock()
	if !already {
		_ = s.ForceKeyframe()
	}
}

func (s *Sender) BroadcastVideoInfo() {
	s.cfgMu.RLock()
	width := uint32(s.cfg.Capture.Width)
	height := uint32(s.cfg.Capture.Height)
	fps := uint32(s.cfg.Capture.FPS)
	gridSize := uint32(10)
	if v, ok := s.cfg.Capture.RGBACodecConfig["tile_size"]; ok {
		if val, ok := v.(int); ok {
			gridSize = uint32(val)
		} else if val, ok := v.(float64); ok {
			gridSize = uint32(val)
		}
	}
	codecName := s.codecName
	s.cfgMu.RUnlock()

	packet := stream.MarshalVideoInfo(width, height, fps, gridSize, codecName)
	for _, viewer := range s.activeViewers() {
		if viewer != nil && viewer.addr != nil {
			_, _ = s.conn.WriteToUDP(packet, viewer.addr)
		}
	}
}

func (s *Sender) FlushAllVideoQueues() {
	for _, viewer := range s.activeViewers() {
		if viewer != nil {
			viewer.flushVideo()
		}
	}
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

// broadcastVideoBatch is used by independently decodable media (the block/tile
// path). H.264 must use broadcastH264Batch because arbitrary P-frame shedding
// breaks the reference chain until the next IDR.
func (s *Sender) broadcastVideoBatch(packets [][]byte, frameSeq uint32) {
	if len(packets) == 0 {
		return
	}
	deadline := time.Now().Add(s.frameDeadline)
	fecCache := make(map[int][][]byte)
	for _, viewer := range s.activeViewers() {
		if viewer.shouldDropVideoBatch(frameSeq) {
			atomic.AddUint64(&viewer.droppedVideo, 1)
			continue
		}

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

func (s *Sender) broadcastH264Batch(packets [][]byte, frameSeq uint32, keyframe bool) {
	if len(packets) == 0 {
		return
	}
	deadline := time.Now().Add(s.frameDeadline)
	fecCache := make(map[int][][]byte)
	for _, viewer := range s.activeViewers() {
		viewer.mu.RLock()
		needsKeyframe := viewer.needsKeyframe
		groupSize := viewer.fecGroupSize
		viewer.mu.RUnlock()

		if needsKeyframe && !keyframe {
			atomic.AddUint64(&viewer.droppedVideo, 1)
			continue
		}

		viewerPackets := packets
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

		batch := packetBatch{
			packets:  viewerPackets,
			class:    mediaVideo,
			frameSeq: frameSeq,
			deadline: deadline,
			keyframe: keyframe,
		}
		select {
		case viewer.videoQ <- batch:
			if keyframe {
				viewer.mu.Lock()
				viewer.needsKeyframe = false
				viewer.mu.Unlock()
			}
		default:
			// Never silently discard one reference frame and continue with later
			// dependent P frames. Flush latency, wait for an IDR, and resume there.
			viewer.flushVideo()
			atomic.AddUint64(&viewer.droppedVideo, 1)
			if keyframe {
				select {
				case viewer.videoQ <- batch:
					viewer.mu.Lock()
					viewer.needsKeyframe = false
					viewer.mu.Unlock()
				default:
					s.markViewerNeedsKeyframe(viewer)
				}
			} else {
				s.markViewerNeedsKeyframe(viewer)
			}
		}
	}
}

func (s *Sender) sendVideoBatchTo(addr *net.UDPAddr, packets [][]byte, frameSeq uint32) {
	viewer := s.touchViewer(addr)
	if viewer == nil || len(packets) == 0 {
		return
	}
	if s.codecName != "h264" && viewer.shouldDropVideoBatch(frameSeq) {
		atomic.AddUint64(&viewer.droppedVideo, 1)
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

		// 1. Drain pending audio first (audio has highest scheduling priority).
		for {
			select {
			case batch := <-viewer.audioQ:
				s.sendBatch(viewer, batch)
			default:
				goto audioDrained
			}
		}
	audioDrained:

		// 2. Process a bounded repair budget before yielding to fresh video.
		servicedRepair := false
		select {
		case batch := <-viewer.repairQ:
			s.sendBatch(viewer, batch)
			servicedRepair = true
		default:
		}

		// 3. Service next available batch.
		select {
		case <-s.ctx.Done():
			return
		case <-viewer.done:
			return
		case batch := <-viewer.audioQ:
			s.sendBatch(viewer, batch)
		case batch := <-viewer.videoQ:
			s.sendBatch(viewer, batch)
		case batch := <-viewer.repairQ:
			if !servicedRepair {
				s.sendBatch(viewer, batch)
			}
		}
	}
}

func (s *Sender) sendBatch(viewer *viewerState, batch packetBatch) {
	if !batch.deadline.IsZero() && time.Now().After(batch.deadline) {
		if batch.class == mediaVideo && s.codecName == "h264" {
			s.markViewerNeedsKeyframe(viewer)
		}
		return
	}

	isAudio := batch.class == mediaAudio
	isH264Video := batch.class == mediaVideo && s.codecName == "h264"
	for _, packet := range batch.packets {
		// Once an H.264 AU starts transmission, finish the AU. Cutting it off at
		// the deadline creates a syntactically truncated frame that can never be
		// decoded and poisons the reference chain. Other media/repair remains
		// deadline-aware packet by packet.
		if !isH264Video && !batch.deadline.IsZero() && time.Now().After(batch.deadline) {
			return
		}
		viewer.pacer.pace(len(packet), isAudio)
		_, _ = s.conn.WriteToUDP(packet, viewer.addr)
	}
}
