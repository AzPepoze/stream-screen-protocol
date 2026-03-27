package client

import (
	"log"
	"sort"
	"time"
)

func (r *ClientReceiver) audioLoop() {
	lastLogAt := time.Now()
	playedPackets := uint64(0)
	playedPCMBytes := uint64(0)

	for {
		select {
		case <-r.ctx.Done():
			return
		case payload, ok := <-r.audioFrames:
			if !ok {
				return
			}
			if r.audioDecoder == nil || r.audioPlayer == nil {
				continue
			}
			pcm, err := r.audioDecoder.DecodeToPCM(payload)
			if err != nil {
				log.Printf("Client: audio decode failed: %v", err)
				continue
			}
			if err := r.audioPlayer.PlayPCM(pcm); err != nil {
				log.Printf("Client: audio playback write failed: %v", err)
				continue
			}

			playedPackets++
			playedPCMBytes += uint64(len(pcm))
			now := time.Now()
			if now.Sub(lastLogAt) >= time.Second {
				elapsed := now.Sub(lastLogAt).Seconds()
				if elapsed <= 0 {
					elapsed = 1
				}
				packetsPerSec := float64(playedPackets) / elapsed
				kbps := (float64(playedPCMBytes) * 8 / elapsed) / 1000
				log.Printf("Client: audio playback packets=%d rate=%.1f pkt/s pcm=%.1f kbps", playedPackets, packetsPerSec, kbps)
				lastLogAt = now
				playedPackets = 0
				playedPCMBytes = 0
			}
		}
	}
}

func (r *ClientReceiver) setAudioInfo(sampleRate, channels, frameMS, bitrate uint32, codec string) {
	r.audioInfoMu.Lock()
	r.audioRate = sampleRate
	r.audioChannels = channels
	r.audioFrameMS = frameMS
	r.audioBitrate = bitrate
	r.audioCodec = codec
	r.audioInfoMu.Unlock()

	if r.audioDecoder != nil {
		r.audioDecoder.SetFormat(int(sampleRate), int(channels))
	}
}

func (r *ClientReceiver) handleAudioDataPacket(frameSeq uint32, packetID uint32, totalPackets uint32, payload []byte) {
	if !r.audioEnabled {
		return
	}
	if totalPackets <= 1 {
		r.enqueueAudioPayload(payload)
		return
	}

	r.audioFragMu.Lock()
	fb, ok := r.audioFragBuf[frameSeq]
	if !ok {
		fb = &audioFragmentBuffer{
			fragments:    make(map[uint32][]byte),
			totalPackets: totalPackets,
			receivedAt:   time.Now(),
		}
		r.audioFragBuf[frameSeq] = fb
	}
	frag := make([]byte, len(payload))
	copy(frag, payload)
	fb.fragments[packetID] = frag

	// Prune stale fragment buffers periodically.
	now := time.Now()
	for seq, stale := range r.audioFragBuf {
		if now.Sub(stale.receivedAt) > 2*time.Second {
			delete(r.audioFragBuf, seq)
		}
	}

	if uint32(len(fb.fragments)) == fb.totalPackets {
		ids := make([]int, 0, len(fb.fragments))
		for id := range fb.fragments {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)

		totalLen := 0
		for _, id := range ids {
			totalLen += len(fb.fragments[uint32(id)])
		}
		joined := make([]byte, totalLen)
		offset := 0
		for _, id := range ids {
			b := fb.fragments[uint32(id)]
			copy(joined[offset:], b)
			offset += len(b)
		}
		delete(r.audioFragBuf, frameSeq)
		r.audioFragMu.Unlock()

		r.enqueueAudioPayload(joined)
		return
	}
	r.audioFragMu.Unlock()
}

func (r *ClientReceiver) enqueueAudioPayload(payload []byte) {
	if len(payload) == 0 {
		return
	}
	packet := make([]byte, len(payload))
	copy(packet, payload)

	select {
	case r.audioFrames <- packet:
		return
	default:
	}

	select {
	case <-r.audioFrames:
	default:
	}

	select {
	case r.audioFrames <- packet:
	default:
		log.Printf("Client: audio queue full, dropping packet")
	}
}
