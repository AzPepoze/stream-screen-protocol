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
	packets      map[uint32][]byte
	totalPackets uint32
	receivedAt   time.Time
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

func (jb *JitterBuffer) MarkRecoveredByFEC(frameSeq, packetID uint32) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	key := packetKey{frameSeq: frameSeq, packetID: packetID}
	if _, ok := jb.activeMissing[key]; ok {
		delete(jb.activeMissing, key)
		if jb.lossObserver != nil {
			jb.lossObserver.OnMissingRecoveredByFEC(1)
		}
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

	key := packetKey{frameSeq: header.FrameSeq, packetID: header.PacketID}
	if _, ok := jb.activeMissing[key]; ok {
		delete(jb.activeMissing, key)
		if jb.lossObserver != nil {
			jb.lossObserver.OnMissingRecoveredByNACK(1)
		}
	}

	fb, ok := jb.frames[header.FrameSeq]
	if !ok {
		fb = &FrameBuffer{
			packets:      make(map[uint32][]byte),
			totalPackets: header.TotalPackets,
			receivedAt:   time.Now(),
		}
		jb.frames[header.FrameSeq] = fb
	}

	cp := make([]byte, len(payload))
	copy(cp, payload)
	fb.packets[header.PacketID] = cp

	// Track newly detected missing packets for this frame
	for i := uint32(0); i < header.TotalPackets; i++ {
		if _, have := fb.packets[i]; !have {
			mKey := packetKey{frameSeq: header.FrameSeq, packetID: i}
			if _, tracked := jb.activeMissing[mKey]; !tracked {
				jb.activeMissing[mKey] = struct{}{}
				if jb.lossObserver != nil {
					jb.lossObserver.OnUniqueMissingDetected(1)
				}
			}
		}
	}

	now := time.Now()

	if uint32(len(fb.packets)) == fb.totalPackets {
		data := jb.reassemble(fb)
		delete(jb.frames, header.FrameSeq)
		delete(jb.nackedFrames, header.FrameSeq)
		jb.cleanActiveMissingForFrame(header.FrameSeq)
		jb.checkPendingFrames(now, header.FrameSeq)
		return data, header.FrameSeq
	}

	received := float64(len(fb.packets)) / float64(fb.totalPackets)
	if jb.allowPartial && received >= jb.partialFrameReady {
		data := jb.reassemble(fb)
		delete(jb.frames, header.FrameSeq)
		delete(jb.nackedFrames, header.FrameSeq)
		jb.cleanActiveMissingForFrame(header.FrameSeq)
		logger.Info("client", "block=%d ready %.0f%% (%d/%d packets)", header.FrameSeq, received*100, len(fb.packets), fb.totalPackets)
		jb.checkPendingFrames(now, header.FrameSeq)
		return data, header.FrameSeq
	}

	return jb.checkPendingFrames(now, 0)
}

func (jb *JitterBuffer) checkPendingFrames(now time.Time, excludeSeq uint32) ([]byte, uint32) {
	for seq, frame := range jb.frames {
		if seq == excludeSeq {
			continue
		}
		age := now.Sub(frame.receivedAt)
		missing := jb.getMissing(frame)

		// Request loss as soon as the initial/retry delay has elapsed.
		if len(missing) > 0 && age >= jb.nackRetryDelay && age <= jb.maxLatency {
			lastNACK, already := jb.nackedFrames[seq]
			if !already || now.Sub(lastNACK) >= jb.nackRetryDelay {
				request := NACKRequest{FrameSeq: seq, PacketIDs: missing}
				select {
				case jb.nackChan <- request:
					jb.nackedFrames[seq] = now
				default:
					// Never block the packet receive path behind a saturated repair queue.
				}
			}
		}

		if age <= jb.maxLatency {
			continue
		}

		receivedRatio := float64(len(frame.packets)) / float64(frame.totalPackets)
		if jb.forceOutput && receivedRatio > 0 {
			data := jb.reassemble(frame)
			delete(jb.frames, seq)
			delete(jb.nackedFrames, seq)
			jb.markFrameExpired(seq, frame)
			logger.Info("client", "FORCE output frame=%d %.0f%% (%d/%d packets, %d missing)", seq, receivedRatio*100, len(frame.packets), frame.totalPackets, len(missing))
			return data, seq
		}

		// Complete-frame codecs such as H.264 cannot decode a truncated access
		// unit. Discard expired frame and mark remaining missing packets unrecovered.
		if age > jb.maxLatency*2 {
			delete(jb.frames, seq)
			delete(jb.nackedFrames, seq)
			jb.markFrameExpired(seq, frame)
		}
	}

	return nil, 0
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
		if _, ok := jb.activeMissing[k]; ok {
			delete(jb.activeMissing, k)
			if jb.lossObserver != nil {
				jb.lossObserver.OnMissingUnrecovered(1)
			}
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
