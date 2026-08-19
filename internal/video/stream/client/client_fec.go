package client

import (
	"sync"
	"time"

	"streamscreen/internal/video/stream"
)

type recoveredPacket struct {
	Header  stream.PacketHeader
	Payload []byte
}

type fecFrameState struct {
	packets map[uint32][]byte
	groups  map[uint32]stream.XORFEC
	updated time.Time
}

// FECRecoverer performs single-erasure recovery for each XOR parity group.
// It is intentionally independent of the jitter buffer: recovered packets are
// re-injected through the exact same jitter/assembly path as network packets.
type FECRecoverer struct {
	mu     sync.Mutex
	frames map[uint32]*fecFrameState
	maxAge time.Duration
}

func NewFECRecoverer(maxAge time.Duration) *FECRecoverer {
	if maxAge <= 0 {
		maxAge = 500 * time.Millisecond
	}
	return &FECRecoverer{frames: make(map[uint32]*fecFrameState), maxAge: maxAge}
}

func (r *FECRecoverer) PushData(h stream.PacketHeader, payload []byte) []recoveredPacket {
	if h.PacketType != stream.CSPPacketTypeData {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	state := r.frame(h.FrameSeq)
	state.packets[h.PacketID] = append([]byte(nil), payload...)
	state.updated = time.Now()
	recovered := r.recoverReady(state)
	r.cleanupLocked()
	return recovered
}

func (r *FECRecoverer) PushFEC(packet []byte) ([]recoveredPacket, error) {
	fec, err := stream.UnmarshalXORFEC(packet)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.frame(fec.FrameSeq)
	state.groups[fec.GroupStart] = fec
	state.updated = time.Now()
	recovered := r.recoverReady(state)
	r.cleanupLocked()
	return recovered, nil
}

func (r *FECRecoverer) ForgetFrame(frameSeq uint32) {
	r.mu.Lock()
	delete(r.frames, frameSeq)
	r.mu.Unlock()
}

func (r *FECRecoverer) frame(frameSeq uint32) *fecFrameState {
	state := r.frames[frameSeq]
	if state == nil {
		state = &fecFrameState{
			packets: make(map[uint32][]byte),
			groups:  make(map[uint32]stream.XORFEC),
			updated: time.Now(),
		}
		r.frames[frameSeq] = state
	}
	return state
}

func (r *FECRecoverer) recoverReady(state *fecFrameState) []recoveredPacket {
	var recovered []recoveredPacket
	for groupStart, fec := range state.groups {
		missingID := uint32(0)
		missingIndex := -1
		missingCount := 0
		for i := range fec.Lengths {
			packetID := groupStart + uint32(i)
			if _, ok := state.packets[packetID]; !ok {
				missingID = packetID
				missingIndex = i
				missingCount++
				if missingCount > 1 {
					break
				}
			}
		}
		if missingCount != 1 {
			continue
		}

		payload := append([]byte(nil), fec.Parity...)
		for i := range fec.Lengths {
			packetID := groupStart + uint32(i)
			if packetID == missingID {
				continue
			}
			known, ok := state.packets[packetID]
			if !ok {
				missingCount++
				break
			}
			for j, b := range known {
				if j < len(payload) {
					payload[j] ^= b
				}
			}
		}
		if missingCount != 1 || missingIndex < 0 {
			continue
		}
		wantLen := int(fec.Lengths[missingIndex])
		if wantLen > len(payload) {
			continue
		}
		payload = payload[:wantLen]
		state.packets[missingID] = append([]byte(nil), payload...)
		recovered = append(recovered, recoveredPacket{
			Header: stream.PacketHeader{
				Version:      stream.CSPVersion,
				PacketType:   stream.CSPPacketTypeData,
				FrameSeq:     fec.FrameSeq,
				PacketID:     missingID,
				TotalPackets: fec.TotalPackets,
				Timestamp:    fec.Timestamp,
			},
			Payload: payload,
		})
		delete(state.groups, groupStart)
	}
	return recovered
}

func (r *FECRecoverer) cleanupLocked() {
	cutoff := time.Now().Add(-r.maxAge)
	for frameSeq, state := range r.frames {
		if state.updated.Before(cutoff) {
			delete(r.frames, frameSeq)
		}
	}
}
