package opus

import (
	"fmt"
	"io"
	"os/exec"

	"streamscreen/internal/config"
)

type Encoder struct {
	sampleRate  int
	channels    int
	frameMS     int
	bitrateKbps int
}

type Decoder struct {
	sampleRate int
	channels   int
}

func NewEncoder(cfg config.ServerConfig) (*Encoder, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("audio encoder requires ffmpeg in PATH: %w", err)
	}
	return &Encoder{
		sampleRate:  cfg.Audio.SampleRate,
		channels:    cfg.Audio.Channels,
		frameMS:     cfg.Audio.FrameMS,
		bitrateKbps: cfg.Audio.BitrateKbps,
	}, nil
}

func NewDecoder(cfg config.ClientConfig) (*Decoder, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("audio decoder requires ffmpeg in PATH: %w", err)
	}
	sampleRate := 48000
	channels := 2
	return &Decoder{
		sampleRate: sampleRate,
		channels:   channels,
	}, nil
}

func (e *Encoder) EncodePCM(pcm []byte) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, fmt.Errorf("audio encoder: empty pcm")
	}
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", e.sampleRate),
		"-ac", fmt.Sprintf("%d", e.channels),
		"-i", "pipe:0",
		"-c:a", "libopus",
		"-application", "lowdelay",
		"-frame_duration", fmt.Sprintf("%d", e.frameMS),
		"-b:a", fmt.Sprintf("%dk", e.bitrateKbps),
		"-vbr", "off",
		"-f", "ogg",
		"pipe:1",
	}
	return runFFmpegWithInput(args, pcm, "audio encoder")
}

func (d *Decoder) DecodeToPCM(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("audio decoder: empty payload")
	}
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "error",
		"-f", "ogg",
		"-i", "pipe:0",
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ar", fmt.Sprintf("%d", d.sampleRate),
		"-ac", fmt.Sprintf("%d", d.channels),
		"pipe:1",
	}
	return runFFmpegWithInput(args, payload, "audio decoder")
}

func (d *Decoder) SetFormat(sampleRate, channels int) {
	if sampleRate > 0 {
		d.sampleRate = sampleRate
	}
	if channels > 0 {
		d.channels = channels
	}
}

func (e *Encoder) Close() error { return nil }

func (d *Decoder) Close() error { return nil }

func runFFmpegWithInput(args []string, in []byte, opName string) ([]byte, error) {
	cmd := exec.Command("ffmpeg", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%s stdin pipe: %w", opName, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s stdout pipe: %w", opName, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("%s stderr pipe: %w", opName, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s start failed: %w", opName, err)
	}

	if _, err := stdin.Write(in); err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, fmt.Errorf("%s write failed: %w", opName, err)
	}
	_ = stdin.Close()

	out, readErr := io.ReadAll(stdout)
	errOut, _ := io.ReadAll(stderr)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("%s stdout read failed: %w", opName, readErr)
	}
	if waitErr != nil {
		if len(errOut) > 0 {
			return nil, fmt.Errorf("%s failed: %s", opName, string(errOut))
		}
		return nil, fmt.Errorf("%s failed: %w", opName, waitErr)
	}
	if len(out) == 0 {
		if len(errOut) > 0 {
			return nil, fmt.Errorf("%s produced no output: %s", opName, string(errOut))
		}
		return nil, fmt.Errorf("%s produced no output", opName)
	}
	return out, nil
}
