//go:build (linux || windows) && cgo

package playback

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/gen2brain/malgo"
	"streamscreen/internal/config"
)

type miniaudioPlayer struct {
	ctx         *malgo.AllocatedContext
	device      *malgo.Device
	mu          sync.Mutex
	pending     []byte
	maxBuffered int
	closed      bool
	deviceIDPtr unsafe.Pointer
}

func newPlayer(cfg config.ClientConfig) (Player, error) {
	sampleRate := 48000
	channels := 2
	outputDevice := cfg.Audio.OutputDevice
	if outputDevice == "" {
		outputDevice = "default"
	}
	if cfg.CodecConfig != nil {
		if v, ok := cfg.CodecConfig["audio_sample_rate"].(float64); ok && int(v) > 0 {
			sampleRate = int(v)
		}
		if v, ok := cfg.CodecConfig["audio_channels"].(float64); ok && int(v) > 0 {
			channels = int(v)
		}
		if outputDevice == "default" {
			if v, ok := cfg.CodecConfig["audio_output_device"].(string); ok && v != "" {
				outputDevice = v
			}
		}
	}

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return nil, fmt.Errorf("miniaudio init context failed: %w", err)
	}

	cfgDevice := malgo.DefaultDeviceConfig(malgo.Playback)
	cfgDevice.Playback.Format = malgo.FormatS16
	cfgDevice.Playback.Channels = uint32(channels)
	cfgDevice.SampleRate = uint32(sampleRate)
	cfgDevice.Alsa.NoMMap = 1

	devicePtr, selectedName, err := selectPlaybackDevice(ctx.Context, outputDevice)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, err
	}
	if devicePtr != nil {
		cfgDevice.Playback.DeviceID = devicePtr
	}

	p := &miniaudioPlayer{
		ctx:         ctx,
		maxBuffered: sampleRate * channels * 2 / 2, // 500ms max queue
		deviceIDPtr: devicePtr,
	}
	fmt.Printf("[audio] playback output device: %s\n", selectedName)

	callbacks := malgo.DeviceCallbacks{
		Data: func(outputSamples, _ []byte, _ uint32) {
			p.onData(outputSamples)
		},
	}

	device, err := malgo.InitDevice(ctx.Context, cfgDevice, callbacks)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("miniaudio init device failed: %w", err)
	}
	p.device = device

	if err := p.device.Start(); err != nil {
		p.device.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("miniaudio start device failed: %w", err)
	}

	return p, nil
}

func (p *miniaudioPlayer) onData(output []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		for i := range output {
			output[i] = 0
		}
		return
	}

	n := copy(output, p.pending)
	if n > 0 {
		p.pending = p.pending[n:]
	}
	for i := n; i < len(output); i++ {
		output[i] = 0
	}
}

func (p *miniaudioPlayer) PlayPCM(pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("audio playback closed")
	}

	if len(p.pending)+len(pcm) > p.maxBuffered {
		over := len(p.pending) + len(pcm) - p.maxBuffered
		if over >= len(p.pending) {
			p.pending = p.pending[:0]
		} else {
			p.pending = p.pending[over:]
		}
	}

	p.pending = append(p.pending, pcm...)
	return nil
}

func (p *miniaudioPlayer) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.pending = nil
	device := p.device
	ctx := p.ctx
	p.device = nil
	p.ctx = nil
	p.deviceIDPtr = nil
	p.mu.Unlock()

	if device != nil {
		_ = device.Stop()
		device.Uninit()
	}
	if ctx != nil {
		_ = ctx.Uninit()
		ctx.Free()
	}
	return nil
}

func selectPlaybackDevice(ctx malgo.Context, desired string) (unsafe.Pointer, string, error) {
	desired = strings.TrimSpace(desired)
	if desired == "" || desired == "default" {
		return nil, "default", nil
	}

	devices, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, "", fmt.Errorf("list playback devices failed: %w", err)
	}
	if len(devices) == 0 {
		return nil, "", fmt.Errorf("no playback devices found")
	}

	if desired == "interactive" {
		idx, err := choosePlaybackDeviceInteractive(devices)
		if err != nil {
			return nil, "", err
		}
		if idx < 0 {
			return nil, "default", nil
		}
		ptr := devices[idx].ID.Pointer()
		return ptr, devices[idx].Name(), nil
	}

	for i := range devices {
		name := devices[i].Name()
		id := devices[i].ID.String()
		if strings.EqualFold(name, desired) || strings.EqualFold(id, desired) {
			ptr := devices[i].ID.Pointer()
			return ptr, name, nil
		}
	}
	return nil, "", fmt.Errorf("playback device not found: %s", desired)
}

func choosePlaybackDeviceInteractive(devices []malgo.DeviceInfo) (int, error) {
	fmt.Println("[audio] Select playback output device:")
	fmt.Println("  1) default")
	for i := range devices {
		mark := ""
		if devices[i].IsDefault != 0 {
			mark = " (system default)"
		}
		fmt.Printf("  %d) %s%s\n", i+2, devices[i].Name(), mark)
	}
	fmt.Print("Enter choice number (default 1): ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return -1, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		return -1, fmt.Errorf("invalid playback selection: %s", line)
	}
	if n == 1 {
		return -1, nil
	}
	if n < 2 || n > len(devices)+1 {
		return -1, fmt.Errorf("playback selection out of range: %d", n)
	}
	return n - 2, nil
}
