package client

import (
	"sort"
	"sync"
	"time"

	"streamscreen/internal/logger"
	"streamscreen/internal/video/stream"
)

// TileBuffer stores a frame divided into tile regions for partial display.
type TileBuffer struct {
	tiles      map[uint32][]byte
	totalTiles uint32
	width      int
	height     int
	tileWidth  int
	tileHeight int
}

type LossObserver interface {
	OnUniqueMissingDetected(count int)
	OnMissingRecoveredByNACK(count int)
	OnMissingRecoveredByFEC(count int)
	OnMissingUnrecovered(count int)
}

type packetKey struct {
	frameSeq uint32
	packetID uint32
}

// JitterBuffer reassembles packets into frames and handles missing data.
type JitterBuffer struct {
	mu                sync.Mutex
	frames            map[uint32]*FrameBuffer
	finalizedFrames   map[uint32]time.Time
	maxLatency        time.Duration
	lossTolerance     float64
	nackChan          chan NACKRequest
	nackedFrames      map[uint32]time.Time
	nackRetryDelay    time.Duration
	partialFrameReady float64
	allowPartial      bool
	forceOutput       bool
	lossObserver      LossObserver
	activeMissing     map[packetKey]struct{}
}

type FrameBuffer struct {
	packets         map[uint32][]byte
	totalPackets    uint32
	receivedAt      time.Time
	lastPacketAt    time.Time
	highestPacketID uint32
	highestSet      bool
}

type NACKRequest struct {
	FrameSeq  uint32
	PacketIDs []uint32
}

type assembledFrame struct {
	Seq  uint32
	Data []byte
}

type JitterBufferOptions struct {
	MaxLatency        time.Duration
	LossTolerance     float64
	NackRetryDelay    time.Duration
	PartialFrameReady float64
	AllowPartial      bool
	ForceOutput       bool
	LossObserver      LossObserver
}

func NewJitterBuffer(maxLatency time.Duration, lossTolerance float64) *JitterBuffer {
	return NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        maxLatency,
		LossTolerance:     lossTolerance,
		NackRetryDelay:    8 * time.Millisecond,
		PartialFrameReady: 0.98,
		AllowPartial:      true,
		ForceOutput:       true,
	})
}

func NewJitterBufferWithOptions(opts JitterBufferOptions) *JitterBuffer {
	if opts.MaxLatency <= 0 {
		opts.MaxLatency = 80 * time.Millisecond
	}
	if opts.LossTolerance <= 0 {
		opts.LossTolerance = 0.1
	}
	if opts.NackRetryDelay <= 0 {
		opts.NackRetryDelay = 8 * time.Millisecond
	}
	if opts.PartialFrameReady <= 0 || opts.PartialFrameReady > 1 {
		opts.PartialFrameReady = 0.98
	}
	return &JitterBuffer{
		frames:            make(map[uint32]*FrameBuffer),
		finalizedFrames:   make(map[uint32]time.Time),
		maxLatency:        opts.MaxLatency,
		lossTolerance:     opts.LossTolerance,
		nackChan:          make(chan NACKRequest, 100),
		nackedFrames:      make(map[uint32]time.Time),
		nackRetryDelay:    opts.NackRetryDelay,
		partialFrameReady: opts.PartialFrameReady,
		allowPartial:      opts.AllowPartial,
		forceOutput:       opts.ForceOutput,
		lossObserver:      opts.LossObserver,
		activeMissing:     make(map[packetKey]struct{}),
	}
}

func (jb *JitterBuffer) SetLossObserver(obs LossObserver) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.lossObserver = obs
}

// MarkRecoveredByFEC records a real media loss recovered by parity. FEC may
// recover a packet before the NACK path has observed the gap, so make the loss
// event explicit here instead of requiring it to have been active already.
func (jb *JitterBuffer) MarkRecoveredByFEC(frameSeq, packetID uint32) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	if _, finalized := jb.finalizedFrames[frameSeq]; finalized {
		return
	}
	key := packetKey{frameSeq: frameSeq, packetID: packetID}
	if _, ok := jb.activeMissing[key]; ok {
		delete(jb.activeMissing, key)
	} else if jb.lossObserver != nil {
		jb.lossObserver.OnUniqueMissingDetected(1)
	}
	if jb.lossObserver != nil {
		jb.lossObserver.OnMissingRecoveredByFEC(1)
	}
}

func (jb *JitterBuffer) SetCompleteFramesOnly() {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.allowPartial = false
	jb.forceOutput = false
}

func (jb *JitterBuffer) SetAllowPartial(allow bool, force bool) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.allowPartial = allow
	jb.forceOutput = force
}

func (jb *JitterBuffer) Flush() {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.frames = make(map[uint32]*FrameBuffer)
	jb.finalizedFrames = make(map[uint32]time.Time)
	jb.nackedFrames = make(map[uint32]time.Time)
	jb.activeMissing = make(map[packetKey]struct{})
}

func (jb *JitterBuffer) ConfigureTiming(maxLatency, nackRetryDelay time.Duration) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	if maxLatency > 0 {
		jb.maxLatency = maxLatency
	}
	if nackRetryDelay > 0 {
		jb.nackRetryDelay = nackRetryDelay
	}
}

func (jb *JitterBuffer) Push(header stream.PacketHeader, payload []byte) (readyData []byte, readySeq uint32) {
	jb.mu.Lock()
	defer jb.mu.Unlock()

	now := time.Now()
	jb.pruneFinalized(now)
	if _, finalized := jb.finalizedFrames[header.FrameSeq]; finalized {
		// A delayed original, repair retransmission, or duplicate must never
		// resurrect a frame that was already emitted/abandoned.
		return nil, 0
	}

	fb, ok := jb.frames[header.FrameSeq]
	if ok {
		if _, duplicate := fb.packets[header.PacketID]; duplicate {
			// Do not let duplicates reset the reorder/NACK quiet timer.
			return nil, 0
		}
	} else {
		fb = &FrameBuffer{
			packets:      make(map[uint32][]byte),
			totalPackets: header.TotalPackets,
			receivedAt:   now,
		}
		jb.frames[header.FrameSeq] = fb
	}

	key := packetKey{frameSeq: header.FrameSeq, packetID: header.PacketID}
	if _, missing := jb.activeMissing[key]; missing {
		delete(jb.activeMissing, key)
		if jb.lossObserver != nil {
			jb.lossObserver.OnMissingRecoveredByNACK(1)
		}
	}

	fb.lastPacketAt = now
	if !fb.highestSet || header.PacketID > fb.highestPacketID {
		fb.highestPacketID = header.PacketID
		fb.highestSet = true
	}

	cp := make([]byte, len(payload))
	copy(cp, payload)
	fb.packets[header.PacketID] = cp

	if uint32(len(fb.packets)) == fb.totalPackets {
		data := jb.reassemble(fb)
		delete(jb.frames, header.FrameSeq)
		delete(jb.nackedFrames, header.FrameSeq)
		jb.cleanActiveMissingForFrame(header.FrameSeq)
		jb.finalizeFrame(header.FrameSeq, now)
		jb.checkPendingFrames(now, header.FrameSeq)
		return data, header.FrameSeq
	}

	received := float64(len(fb.packets)) / float64(fb.totalPackets)
	if jb.allowPartial && received >= jb.partialFrameReady {
		data := jb.reassemble(fb)
		delete(jb.frames, header.FrameSeq)
		delete(jb.nackedFrames, header.FrameSeq)
		jb.cleanActiveMissingForFrame(header.FrameSeq)
		jb.finalizeFrame(header.FrameSeq, now)
		logger.Info("client", "block=%d ready %.0f%% (%d/%d packets)", header.FrameSeq, received*100, len(fb.packets), fb.totalPackets)
		jb.checkPendingFrames(now, header.FrameSeq)
		return data, header.FrameSeq
	}

	return jb.checkPendingFrames(now, header.FrameSeq)
}

// checkPendingFrames only declares loss after there is evidence that a packet
// should already have arrived. For the newest frame that means a hole behind
// the highest packet ID observed. Once a newer frame is observed, tail holes
// in the older frame also become eligible. The quiet-period guard absorbs
// normal serialization and small UDP reordering before a NACK/loss event.
func (jb *JitterBuffer) checkPendingFrames(now time.Time, newestSeq uint32) ([]byte, uint32) {
	for seq, frame := range jb.frames {
		age := now.Sub(frame.receivedAt)
		frameClosed := newestSeq != 0 && seqBefore(seq, newestSeq)
		missing := jb.getNACKableMissing(frame, frameClosed)

		quietLongEnough := !frame.lastPacketAt.IsZero() && now.Sub(frame.lastPacketAt) >= jb.nackRetryDelay
		if len(missing) > 0 && quietLongEnough && age <= jb.maxLatency {
			lastNACK, already := jb.nackedFrames[seq]
			if !already || now.Sub(lastNACK) >= jb.nackRetryDelay {
				request := NACKRequest{FrameSeq: seq, PacketIDs: missing}
				select {
				case jb.nackChan <- request:
					jb.markMissingDetected(seq, missing)
					jb.nackedFrames[seq] = now
				default:
					// Never block the packet receive path behind a saturated repair queue.
				}
			}
		}

		if age <= jb.maxLatency {
			continue
		}

		allMissing := jb.getMissing(frame)
		receivedRatio := float64(len(frame.packets)) / float64(frame.totalPackets)
		if jb.forceOutput && receivedRatio > 0 {
			data := jb.reassemble(frame)
			delete(jb.frames, seq)
			delete(jb.nackedFrames, seq)
			jb.markFrameExpired(seq, frame)
			jb.finalizeFrame(seq, now)
			logger.Info("client", "FORCE output frame=%d %.0f%% (%d/%d packets, %d missing)", seq, receivedRatio*100, len(frame.packets), frame.totalPackets, len(allMissing))
			return data, seq
		}

		// Complete-frame codecs such as H.264 cannot decode a truncated access
		// unit. Discard expired frame and mark remaining missing packets unrecovered.
		if age > jb.maxLatency*2 {
			delete(jb.frames, seq)
			delete(jb.nackedFrames, seq)
			jb.markFrameExpired(seq, frame)
			jb.finalizeFrame(seq, now)
		}
	}

	return nil, 0
}

func seqBefore(a, b uint32) bool {
	return a != b && int32(a-b) < 0
}

func (jb *JitterBuffer) markMissingDetected(seq uint32, ids []uint32) {
	for _, id := range ids {
		key := packetKey{frameSeq: seq, packetID: id}
		if _, tracked := jb.activeMissing[key]; tracked {
			continue
		}
		jb.activeMissing[key] = struct{}{}
		if jb.lossObserver != nil {
			jb.lossObserver.OnUniqueMissingDetected(1)
		}
	}
}

func (jb *JitterBuffer) getNACKableMissing(fb *FrameBuffer, frameClosed bool) []uint32 {
	if fb == nil || fb.totalPackets == 0 || !fb.highestSet {
		return nil
	}
	last := fb.highestPacketID
	if frameClosed {
		last = fb.totalPackets - 1
	}
	if last >= fb.totalPackets {
		last = fb.totalPackets - 1
	}
	missing := make([]uint32, 0)
	for i := uint32(0); i <= last; i++ {
		if _, ok := fb.packets[i]; !ok {
			missing = append(missing, i)
		}
	}
	return missing
}

func (jb *JitterBuffer) cleanActiveMissingForFrame(seq uint32) {
	for k := range jb.activeMissing {
		if k.frameSeq == seq {
			delete(jb.activeMissing, k)
		}
	}
}

func (jb *JitterBuffer) markFrameExpired(seq uint32, fb *FrameBuffer) {
	missing := jb.getMissing(fb)
	for _, id := range missing {
		k := packetKey{frameSeq: seq, packetID: id}
		if _, ok := jb.activeMissing[k]; !ok {
			jb.activeMissing[k] = struct{}{}
			if jb.lossObserver != nil {
				jb.lossObserver.OnUniqueMissingDetected(1)
			}
		}
		delete(jb.activeMissing, k)
		if jb.lossObserver != nil {
			jb.lossObserver.OnMissingUnrecovered(1)
		}
	}
}

func (jb *JitterBuffer) finalizeFrame(seq uint32, now time.Time) {
	jb.finalizedFrames[seq] = now
}

func (jb *JitterBuffer) pruneFinalized(now time.Time) {
	ttl := jb.maxLatency * 4
	if ttl < time.Second {
		ttl = time.Second
	}
	for seq, finalizedAt := range jb.finalizedFrames {
		if now.Sub(finalizedAt) > ttl {
			delete(jb.finalizedFrames, seq)
		}
	}
}

func (jb *JitterBuffer) reassemble(fb *FrameBuffer) []byte {
	ids := make([]int, 0, len(fb.packets))
	for id := range fb.packets {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)

	totalLen := 0
	for _, id := range ids {
		totalLen += len(fb.packets[uint32(id)])
	}
	res := make([]byte, totalLen)
	offset := 0
	for _, id := range ids {
		p := fb.packets[uint32(id)]
		copy(res[offset:], p)
		offset += len(p)
	}
	return res
}

func (jb *JitterBuffer) getMissing(fb *FrameBuffer) []uint32 {
	missing := make([]uint32, 0)
	for i := uint32(0); i < fb.totalPackets; i++ {
		if _, ok := fb.packets[i]; !ok {
			missing = append(missing, i)
		}
	}
	return missing
}
