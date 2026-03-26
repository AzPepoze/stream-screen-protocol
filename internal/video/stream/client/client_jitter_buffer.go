package client

import (
	"log"
	"sort"
	"streamscreen/internal/video/stream"
	"sync"
	"time"
)

// TileBuffer stores a frame divided into tile regions for partial display
type TileBuffer struct {
	tiles      map[uint32][]byte // tile index -> pixel data
	totalTiles uint32
	width      int
	height     int
	tileWidth  int
	tileHeight int
}

// JitterBuffer reassembles packets into frames and handles missing data.
type JitterBuffer struct {
	mu                sync.Mutex
	frames            map[uint32]*FrameBuffer
	maxLatency        time.Duration
	lossTolerance     float64
	nackChan          chan NACKRequest
	nackedFrames      map[uint32]time.Time // Track which frames we've already NACKed
	nackRetryDelay    time.Duration        // Minimum delay between NACKs for same frame
	partialFrameReady float64              // Accept partial frames at this threshold (e.g., 0.8 = 80%)
	allowPartial      bool
	forceOutput       bool
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
}

func NewJitterBuffer(maxLatency time.Duration, lossTolerance float64) *JitterBuffer {
	return NewJitterBufferWithOptions(JitterBufferOptions{
		MaxLatency:        maxLatency,
		LossTolerance:     lossTolerance,
		NackRetryDelay:    20 * time.Millisecond,
		PartialFrameReady: 0.98,
		AllowPartial:      true,
		ForceOutput:       true,
	})
}

func NewJitterBufferWithOptions(opts JitterBufferOptions) *JitterBuffer {
	if opts.MaxLatency <= 0 {
		opts.MaxLatency = 200 * time.Millisecond
	}
	if opts.LossTolerance <= 0 {
		opts.LossTolerance = 0.1
	}
	if opts.NackRetryDelay <= 0 {
		opts.NackRetryDelay = 20 * time.Millisecond
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
	}
}

func (jb *JitterBuffer) SetCompleteFramesOnly() {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.allowPartial = false
	jb.forceOutput = false
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

	fb, ok := jb.frames[header.FrameSeq]
	if !ok {
		fb = &FrameBuffer{
			packets:      make(map[uint32][]byte),
			totalPackets: header.TotalPackets,
			receivedAt:   time.Now(),
		}
		jb.frames[header.FrameSeq] = fb
	}

	// Copy payload to avoid referencing the shared read buffer which
	// is reused by subsequent ReadFromUDP calls.
	cp := make([]byte, len(payload))
	copy(cp, payload)
	fb.packets[header.PacketID] = cp

	// Check if frame is complete
	if uint32(len(fb.packets)) == fb.totalPackets {
		data := jb.reassemble(fb)
		delete(jb.frames, header.FrameSeq)
		return data, header.FrameSeq
	}

	// Check if frame is "ready enough" (partial frame threshold)
	received := float64(len(fb.packets)) / float64(fb.totalPackets)
	if jb.allowPartial && received >= jb.partialFrameReady {
		data := jb.reassemble(fb)
		delete(jb.frames, header.FrameSeq)
		log.Printf("Client: block=%d ready %.0f%% (%d/%d packets)", header.FrameSeq, received*100, len(fb.packets), fb.totalPackets)
		return data, header.FrameSeq
	}

	// Cleanup old frames and detect missing packets for NACKs
	now := time.Now()
	for seq, f := range jb.frames {
		if now.Sub(f.receivedAt) > jb.maxLatency {
			// Frame expired - force output it rather than drop
			received := float64(len(f.packets)) / float64(f.totalPackets)
			missing := jb.getMissing(f)

			// Optionally output expired frames to prevent video freeze.
			// Disabled for H264 to avoid decoding incomplete frames.
			if jb.forceOutput && received > 0 {
				data := jb.reassemble(f)
				delete(jb.frames, seq)
				delete(jb.nackedFrames, seq)
				log.Printf("Client: FORCE output frame=%d %.0f%% (%d/%d packets, %d missing)", seq, received*100, len(f.packets), f.totalPackets, len(missing))
				return data, seq
			}

			// Send NACK for this frame
			if len(missing) > 0 {
				lastNack, already := jb.nackedFrames[seq]
				if !already || now.Sub(lastNack) > jb.nackRetryDelay {
					log.Printf("Client: NACK request queued frame=%d missing=%d", seq, len(missing))
					jb.nackChan <- NACKRequest{FrameSeq: seq, PacketIDs: missing}
					jb.nackedFrames[seq] = now
				}
			}

			// If we give up on this frame (expired twice as long)
			if now.Sub(f.receivedAt) > jb.maxLatency*2 {
				delete(jb.frames, seq)
				delete(jb.nackedFrames, seq)
			}
		}
	}

	return nil, 0
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
	missing := []uint32{}
	for i := uint32(0); i < fb.totalPackets; i++ {
		if _, ok := fb.packets[i]; !ok {
			missing = append(missing, i)
		}
	}
	return missing
}
