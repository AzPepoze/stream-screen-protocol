package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestColorForCategoryDeterministic(t *testing.T) {
	c1 := ColorForCategory("server")
	c2 := ColorForCategory("server")
	if c1 == "" || c1 != c2 {
		t.Fatalf("expected deterministic color for 'server', got %q and %q", c1, c2)
	}

	c3 := ColorForCategory("SERVER")
	if c1 != c3 {
		t.Fatalf("expected case-insensitive hash, got %q vs %q", c1, c3)
	}

	cClient := ColorForCategory("client")
	if cClient == "" {
		t.Fatalf("expected valid color for 'client'")
	}
}

func TestCategoryOutput(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)

	Info("canvas", "canvas initialized with %dx%d", 1920, 1080)
	out := buf.String()
	if !strings.Contains(out, "[INFO]") || !strings.Contains(out, "[canvas]") || !strings.Contains(out, "canvas initialized with 1920x1080") {
		t.Fatalf("unexpected log output: %q", out)
	}

	buf.Reset()
	Warn("network", "packet loss detected: %d%%", 25)
	out = buf.String()
	if !strings.Contains(out, "[WARN]") || !strings.Contains(out, "[network]") || !strings.Contains(out, "packet loss detected: 25%") {
		t.Fatalf("unexpected log output: %q", out)
	}

	buf.Reset()
	Error("h264", "decode frame failed: %s", "corrupt packet")
	out = buf.String()
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "[h264]") || !strings.Contains(out, "decode frame failed: corrupt packet") {
		t.Fatalf("unexpected log output: %q", out)
	}
}
