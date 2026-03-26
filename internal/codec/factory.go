package codec

import "fmt"

// Encoder and Decoder registries
var encoderFactories = make(map[string]func(Config) (Encoder, error))
var decoderFactories = make(map[string]func(Config) (Decoder, error))

// RegisterEncoder registers an encoder factory
func RegisterEncoder(name string, factory func(Config) (Encoder, error)) {
	encoderFactories[name] = factory
}

// RegisterDecoder registers a decoder factory
func RegisterDecoder(name string, factory func(Config) (Decoder, error)) {
	decoderFactories[name] = factory
}

// NewEncoder creates a new encoder for the specified codec
func NewEncoder(codecName string, cfg Config) (Encoder, error) {
	factory, ok := encoderFactories[codecName]
	if !ok {
		return nil, fmt.Errorf("unknown codec: %s", codecName)
	}
	return factory(cfg)
}

// NewDecoder creates a new decoder for the specified codec
func NewDecoder(codecName string, cfg Config) (Decoder, error) {
	factory, ok := decoderFactories[codecName]
	if !ok {
		return nil, fmt.Errorf("unknown codec: %s", codecName)
	}
	return factory(cfg)
}

// SupportedCodecs returns list of supported codec names
func SupportedCodecs() []string {
	codecs := make([]string, 0, len(encoderFactories))
	for name := range encoderFactories {
		codecs = append(codecs, name)
	}
	return codecs
}

// IsCodecSupported checks if a codec is supported
func IsCodecSupported(codecName string) bool {
	_, ok := encoderFactories[codecName]
	return ok
}
