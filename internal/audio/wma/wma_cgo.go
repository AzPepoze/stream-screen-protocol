//go:build windows && cgo

package wma

/*
#include <stdlib.h>

typedef struct {
    unsigned char *data;
    int size;
} AudioBuffer;

AudioBuffer* EncodeWMA(unsigned char *pcmData, int pcmSize, int sampleRate, int channels, int bitrate);
AudioBuffer* DecodeWMA(unsigned char *encodedData, int encodedSize, int sampleRate, int channels);
void FreeAudioBuffer(AudioBuffer *buffer);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func encodeWMA(pcm []byte, sampleRate, channels, bitrate int) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, fmt.Errorf("empty PCM data")
	}

	cBuffer := C.EncodeWMA(
		(*C.uchar)(unsafe.Pointer(&pcm[0])),
		C.int(len(pcm)),
		C.int(sampleRate),
		C.int(channels),
		C.int(bitrate),
	)
	defer C.FreeAudioBuffer(cBuffer)

	if cBuffer == nil {
		return nil, fmt.Errorf("WMA encoding failed")
	}

	if cBuffer.size <= 0 {
		return nil, fmt.Errorf("WMA encoding produced no output")
	}

	// Copy data from C buffer to Go slice
	result := C.GoBytes(unsafe.Pointer(cBuffer.data), cBuffer.size)
	return result, nil
}

func decodeWMA(encoded []byte, sampleRate, channels int) ([]byte, error) {
	if len(encoded) == 0 {
		return nil, fmt.Errorf("empty encoded data")
	}

	cBuffer := C.DecodeWMA(
		(*C.uchar)(unsafe.Pointer(&encoded[0])),
		C.int(len(encoded)),
		C.int(sampleRate),
		C.int(channels),
	)
	defer C.FreeAudioBuffer(cBuffer)

	if cBuffer == nil {
		return nil, fmt.Errorf("WMA decoding failed")
	}

	if cBuffer.size <= 0 {
		return nil, fmt.Errorf("WMA decoding produced no output")
	}

	// Copy data from C buffer to Go slice
	result := C.GoBytes(unsafe.Pointer(cBuffer.data), cBuffer.size)
	return result, nil
}
