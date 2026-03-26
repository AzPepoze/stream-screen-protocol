package ffmpeg

import "testing"

func TestConfigHelpers(t *testing.T) {
	cfg := map[string]interface{}{
		"int":    5,
		"float":  3.0,
		"string": "x",
	}

	if got := intFrom(cfg, "int", 1); got != 5 {
		t.Fatalf("intFrom int mismatch: %d", got)
	}
	if got := intFrom(cfg, "float", 1); got != 3 {
		t.Fatalf("intFrom float mismatch: %d", got)
	}
	if got := intFrom(cfg, "missing", 7); got != 7 {
		t.Fatalf("intFrom fallback mismatch: %d", got)
	}
	if got := stringFrom(cfg, "string", "z"); got != "x" {
		t.Fatalf("stringFrom mismatch: %s", got)
	}
	if got := stringFrom(cfg, "missing", "z"); got != "z" {
		t.Fatalf("stringFrom fallback mismatch: %s", got)
	}
}
