//go:build windows && cgo

package wma

import (
	"fmt"

	"streamscreen/internal/config"
)

// Encoder provides Windows Media Audio encoding
type Encoder struct {
	sampleRate int
	channels   int
	bitrate    int
}

// Decoder provides Windows Media Audio decoding
type Decoder struct {
	sampleRate int
	channels   int
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
	bitrate := cfg.Audio.BitrateKbps
	if bitrate <= 0 {
		bitrate = 96
	}

	return &Encoder{
		sampleRate: rate,
		channels:   ch,
		bitrate:    bitrate,
	}, nil
}

func NewDecoder(cfg config.ClientConfig) (*Decoder, error) {
	rate := cfg.Audio.SampleRate
	if rate <= 0 {
		rate = 48000
	}
	ch := cfg.Audio.Channels
	if ch <= 0 {
		ch = 2
	}

	return &Decoder{
		sampleRate: rate,
		channels:   ch,
	}, nil
}

// EncodePCM encodes raw PCM to WMA format using Windows Media Foundation
func (e *Encoder) EncodePCM(pcm []byte) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, fmt.Errorf("empty PCM data")
	}
	// Delegate to C implementation
	return encodeWMA(pcm, e.sampleRate, e.channels, e.bitrate)
}

// DecodeToPCM decodes WMA format to raw PCM using Windows Media Foundation
func (d *Decoder) DecodeToPCM(encoded []byte) ([]byte, error) {
	if len(encoded) == 0 {
		return nil, fmt.Errorf("empty encoded data")
	}
	// Delegate to C implementation
	return decodeWMA(encoded, d.sampleRate, d.channels)
}

func (d *Decoder) SetFormat(sampleRate, channels int) {
	d.sampleRate = sampleRate
	d.channels = channels
}

func (e *Encoder) Close() error {
	return nil
}

func (d *Decoder) Close() error {
	return nil
}

func encodeWMA(pcm []byte, sampleRate, channels, bitrate int) ([]byte, error)
func decodeWMA(encoded []byte, sampleRate, channels int) ([]byte, error)
