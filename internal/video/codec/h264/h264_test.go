package h264

import "testing"

func TestBackendFromConfigDefaults(t *testing.T) {
	enc, err := encoderBackendFromConfig(Config{})
	if err != nil {
		t.Fatalf("encoder backend default failed: %v", err)
	}
	if enc == "" {
		t.Fatalf("expected non-empty encoder backend")
	}

	dec, err := decoderBackendFromConfig(Config{})
	if err != nil {
		t.Fatalf("decoder backend default failed: %v", err)
	}
	if dec == "" {
		t.Fatalf("expected non-empty decoder backend")
	}
}

func TestBackendFromConfigInvalidValue(t *testing.T) {
	_, err := encoderBackendFromConfig(Config{"h264_encoder_backend": "bad"})
	if err == nil {
		t.Fatalf("expected invalid encoder backend error")
	}

	_, err = decoderBackendFromConfig(Config{"h264_decoder_backend": "bad"})
	if err == nil {
		t.Fatalf("expected invalid decoder backend error")
	}
}
