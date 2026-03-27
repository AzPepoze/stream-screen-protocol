//go:build cgo

package opus

import (
	"encoding/binary"
	"fmt"
	"sync"

	hropus "gopkg.in/hraban/opus.v2"

	"streamscreen/internal/config"
)

type Encoder struct {
	sampleRate      int
	channels        int
	samplesPerFrame int
	mu              sync.Mutex
	enc             *hropus.Encoder
}

type Decoder struct {
	sampleRate int
	channels   int
	mu         sync.Mutex
	dec        *hropus.Decoder
}

func NewEncoder(cfg config.ServerConfig) (*Encoder, error) {
	rate := cfg.Audio.SampleRate
	if rate <= 0 {
		rate = 48000
	}
	ch := cfg.Audio.Channels
	if ch <= 0 {
		ch = 2
	}
	frameMS := cfg.Audio.FrameMS
	if frameMS <= 0 {
		frameMS = 20
	}
	bitrateKbps := cfg.Audio.BitrateKbps
	if bitrateKbps <= 0 {
		bitrateKbps = 96
	}

	const opusAppAudio = hropus.Application(2049)
	enc, err := hropus.NewEncoder(rate, ch, opusAppAudio)
	if err != nil {
		return nil, fmt.Errorf("audio encoder init failed: %w", err)
	}
	if err := enc.SetBitrate(bitrateKbps * 1000); err != nil {
		return nil, fmt.Errorf("audio encoder bitrate failed: %w", err)
	}

	samplesPerFrame := (rate * frameMS) / 1000
	if samplesPerFrame <= 0 {
		samplesPerFrame = rate / 50
	}

	return &Encoder{
		sampleRate:      rate,
		channels:        ch,
		samplesPerFrame: samplesPerFrame,
		enc:             enc,
	}, nil
}

func NewDecoder(_ config.ClientConfig) (*Decoder, error) {
	rate := 48000
	ch := 2
	dec, err := hropus.NewDecoder(rate, ch)
	if err != nil {
		return nil, fmt.Errorf("audio decoder init failed: %w", err)
	}
	return &Decoder{
		sampleRate: rate,
		channels:   ch,
		dec:        dec,
	}, nil
}

func (e *Encoder) EncodePCM(pcm []byte) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, fmt.Errorf("audio encoder: empty pcm")
	}
	if len(pcm)%2 != 0 {
		return nil, fmt.Errorf("audio encoder: invalid s16le pcm size=%d", len(pcm))
	}

	pcm16 := bytesToInt16(pcm)
	requiredSamples := e.samplesPerFrame * e.channels
	if len(pcm16) != requiredSamples {
		return nil, fmt.Errorf("audio encoder: unexpected frame samples got=%d want=%d", len(pcm16), requiredSamples)
	}

	out := make([]byte, 4000)
	e.mu.Lock()
	n, err := e.enc.Encode(pcm16, out)
	e.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("audio encoder failed: %w", err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("audio encoder produced no output")
	}

	pkt := make([]byte, n)
	copy(pkt, out[:n])
	return pkt, nil
}

func (d *Decoder) DecodeToPCM(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("audio decoder: empty payload")
	}

	// Max opus frame duration is 120ms.
	maxSamplesPerChannel := (d.sampleRate * 120) / 1000
	if maxSamplesPerChannel <= 0 {
		maxSamplesPerChannel = 5760
	}
	pcm16 := make([]int16, maxSamplesPerChannel*d.channels)

	d.mu.Lock()
	n, err := d.dec.Decode(payload, pcm16)
	d.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("audio decoder failed: %w", err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("audio decoder produced no samples")
	}

	totalSamples := n * d.channels
	if totalSamples > len(pcm16) {
		totalSamples = len(pcm16)
	}
	return int16ToBytes(pcm16[:totalSamples]), nil
}

func (d *Decoder) SetFormat(sampleRate, channels int) {
	if sampleRate <= 0 || channels <= 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sampleRate == sampleRate && d.channels == channels {
		return
	}

	dec, err := hropus.NewDecoder(sampleRate, channels)
	if err != nil {
		return
	}
	d.sampleRate = sampleRate
	d.channels = channels
	d.dec = dec
}

func (e *Encoder) Close() error { return nil }

func (d *Decoder) Close() error { return nil }

func bytesToInt16(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2 : i*2+2]))
	}
	return out
}

func int16ToBytes(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(out[i*2:i*2+2], uint16(s))
	}
	return out
}
