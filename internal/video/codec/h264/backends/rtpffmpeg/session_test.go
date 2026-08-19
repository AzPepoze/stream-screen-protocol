package rtpffmpeg

import (
	"bytes"
	"encoding/binary"
	"os/exec"
	"testing"
)

func TestParseRTPPacket(t *testing.T) {
	packet := make([]byte, 12+3)
	packet[0] = 0x80
	packet[1] = 0x80 | 96
	binary.BigEndian.PutUint16(packet[2:4], 7)
	binary.BigEndian.PutUint32(packet[4:8], 1234)
	copy(packet[12:], []byte{0x65, 0xaa, 0xbb})

	headerLen, marker, ts, payload, ok := parseRTPPacket(packet)
	if !ok {
		t.Fatal("packet should parse")
	}
	if headerLen != 12 || !marker || ts != 1234 {
		t.Fatalf("unexpected RTP header: len=%d marker=%v ts=%d", headerLen, marker, ts)
	}
	if len(payload) != 3 || payload[0] != 0x65 {
		t.Fatalf("unexpected payload: %x", payload)
	}
}

func TestAppendSingleNAL(t *testing.T) {
	got, err := appendH264RTPPayload(nil, []byte{0x65, 0x01, 0x02})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 1, 0x65, 0x01, 0x02}
	if string(got) != string(want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestAppendSTAPA(t *testing.T) {
	payload := []byte{24, 0, 2, 0x67, 0x11, 0, 3, 0x68, 0x22, 0x33}
	got, err := appendH264RTPPayload(nil, payload)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 1, 0x67, 0x11, 0, 0, 0, 1, 0x68, 0x22, 0x33}
	if string(got) != string(want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestAppendFUA(t *testing.T) {
	var got []byte
	var err error
	got, err = appendH264RTPPayload(got, []byte{0x7c, 0x85, 0xaa, 0xbb})
	if err != nil {
		t.Fatal(err)
	}
	got, err = appendH264RTPPayload(got, []byte{0x7c, 0x45, 0xcc})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 1, 0x65, 0xaa, 0xbb, 0xcc}
	if string(got) != string(want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestPersistentFFmpegSessionEncodesMultipleFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	const width, height = 64, 64
	session, err := New(Config{
		Codec:       "libx264",
		InputFormat: "rgba",
		FPS:         30,
		Width:       width,
		Height:      height,
		ExtraArgs: []string{
			"-preset", "ultrafast",
			"-tune", "zerolatency",
			"-bf", "0",
			"-g", "30",
			"-x264-params", "aud=1:bframes=0:keyint=30:min-keyint=30:scenecut=0:repeat-headers=1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	pid := session.cmd.Process.Pid

	frame := make([]byte, width*height*4)
	for i := 3; i < len(frame); i += 4 {
		frame[i] = 0xff
	}
	first, err := session.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	frame[0] = 0xff
	second, err := session.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if session.cmd.Process.Pid != pid {
		t.Fatalf("ffmpeg process restarted: first pid=%d current=%d", pid, session.cmd.Process.Pid)
	}
	if !bytes.Contains(first, annexBStartCode) || !bytes.Contains(second, annexBStartCode) {
		t.Fatalf("expected Annex-B H264 output: first=%d bytes second=%d bytes", len(first), len(second))
	}
}
