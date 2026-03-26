package server

import (
	"sync"
)

// PacketBuffer stores recent packets for retransmission.
type PacketBuffer struct {
	mu      sync.RWMutex
	packets map[uint64][]byte // key: (frameSeq << 32) | packetID
	order   []uint64
	maxSize int
}

func NewPacketBuffer(maxSize int) *PacketBuffer {
	return &PacketBuffer{
		packets: make(map[uint64][]byte),
		order:   make([]uint64, 0, maxSize),
		maxSize: maxSize,
	}
}

func (b *PacketBuffer) Put(frameSeq uint32, packetID uint32, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := (uint64(frameSeq) << 32) | uint64(packetID)
	if _, ok := b.packets[key]; !ok {
		if len(b.order) >= b.maxSize {
			oldest := b.order[0]
			delete(b.packets, oldest)
			b.order = b.order[1:]
		}
		b.order = append(b.order, key)
	}
	b.packets[key] = data
}

func (b *PacketBuffer) Get(frameSeq uint32, packetID uint32) []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()

	key := (uint64(frameSeq) << 32) | uint64(packetID)
	return b.packets[key]
}
