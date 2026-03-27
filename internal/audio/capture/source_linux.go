//go:build linux

package capture

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"streamscreen/internal/config"
	"streamscreen/internal/video/portal"
)

const portalAudioChoice = "__portal_screencast_audio__"

func newSource(cfg config.ServerConfig) (Source, error) {
	rate := cfg.Audio.SampleRate
	if rate <= 0 {
		rate = 48000
	}
	ch := cfg.Audio.Channels
	if ch <= 0 {
		ch = 2
	}

	device, err := resolveLinuxInputDevice(cfg)
	if err != nil {
		return nil, err
	}
	if device == portalAudioChoice {
		if _, err := exec.LookPath("pw-record"); err != nil {
			return nil, fmt.Errorf("portal audio capture requires pw-record in PATH: %w", err)
		}
		log.Printf("[audio] linux input source=portal-screencast")
		return newLinuxPortalPWRecordSource(cfg), nil
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("audio capture requires ffmpeg in PATH: %w", err)
	}
	log.Printf("[audio] linux input source=%s", device)
	return newFFmpegSource(cfg, func() *exec.Cmd {
		args := []string{
			"-hide_banner", "-nostdin", "-loglevel", "error",
			"-fflags", "nobuffer",
			"-flags", "low_delay",
			"-f", "pulse",
			"-i", device,
			"-ac", fmt.Sprintf("%d", ch),
			"-ar", fmt.Sprintf("%d", rate),
			"-f", "s16le",
			"pipe:1",
		}
		return exec.Command("ffmpeg", args...)
	})
}

func resolveLinuxInputDevice(cfg config.ServerConfig) (string, error) {
	deviceRaw := audioInputDevice(cfg, "default")
	device := strings.ToLower(strings.TrimSpace(deviceRaw))
	switch device {
	case "interactive":
		return chooseInteractiveLinuxDevice()
	case "portal", "screencast", "portal-screencast":
		return portalAudioChoice, nil
	case "system":
		return detectSystemMonitorSource(), nil
	default:
		return deviceRaw, nil
	}
}

func chooseInteractiveLinuxDevice() (string, error) {
	sources, err := listPulseSources()
	if err != nil || len(sources) == 0 {
		log.Printf("[audio] source discovery failed (%v), fallback to portal option", err)
		return portalAudioChoice, nil
	}

	monitor := detectSystemMonitorSource()
	options := make([]string, 0, len(sources)+3)
	options = append(options,
		"default",
		portalAudioChoice,
	)
	if monitor != "" && monitor != "default" {
		options = append(options, monitor)
	}
	for _, s := range sources {
		duplicate := false
		for _, o := range options {
			if o == s {
				duplicate = true
				break
			}
		}
		if !duplicate {
			options = append(options, s)
		}
	}

	fmt.Println("[audio] Select input source:")
	fmt.Println("  1) default (Pulse/PipeWire default)")
	fmt.Println("  2) portal-screencast-audio (XDG picker, app/system share)")
	if len(options) > 2 {
		fmt.Printf("  3) %s (entire system monitor)\n", options[2])
	}
	for i := 3; i < len(options); i++ {
		fmt.Printf("  %d) %s\n", i+1, options[i])
	}
	fmt.Print("Enter choice number (default 1): ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return options[0], nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(options) {
		log.Printf("[audio] invalid selection %q, fallback to default", line)
		return options[0], nil
	}
	return options[n-1], nil
}

func listPulseSources() ([]string, error) {
	cmd := exec.Command("pactl", "list", "short", "sources")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	sources := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		fields := strings.Split(ln, "\t")
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSpace(fields[1])
		if name != "" {
			sources = append(sources, name)
		}
	}
	return sources, nil
}

func detectSystemMonitorSource() string {
	sources, err := listPulseSources()
	if err != nil {
		return "default"
	}

	cmd := exec.Command("pactl", "get-default-sink")
	if out, err := cmd.Output(); err == nil {
		sink := strings.TrimSpace(string(out))
		if sink != "" {
			candidate := sink + ".monitor"
			for _, src := range sources {
				if src == candidate {
					return src
				}
			}
		}
	}

	for _, src := range sources {
		if strings.HasSuffix(src, ".monitor") {
			return src
		}
	}
	return "default"
}

type linuxPortalPWRecordSource struct {
	cfg       config.ServerConfig
	frames    chan []byte
	session   *portal.ScreenCastSession
	cmd       *exec.Cmd
	stdout    io.ReadCloser
	frameSize int
	cancel    context.CancelFunc
	closeOnce sync.Once
	mu        sync.Mutex
}

func newLinuxPortalPWRecordSource(cfg config.ServerConfig) Source {
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
	frameSize := (rate * ch * 2 * frameMS) / 1000
	if frameSize <= 0 {
		frameSize = 3840
	}

	return &linuxPortalPWRecordSource{
		cfg:       cfg,
		frames:    make(chan []byte, 32),
		frameSize: frameSize,
	}
}

func (s *linuxPortalPWRecordSource) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	session, err := portal.StartScreenCast(runCtx, s.cfg)
	if err != nil {
		return fmt.Errorf("portal screencast failed: %w", err)
	}
	nodeID, ok := pickPortalAudioNode(session.Streams)
	if !ok {
		_ = session.Close()
		return fmt.Errorf("portal did not return any stream node")
	}
	s.session = session

	rate := s.cfg.Audio.SampleRate
	if rate <= 0 {
		rate = 48000
	}
	ch := s.cfg.Audio.Channels
	if ch <= 0 {
		ch = 2
	}

	args := []string{
		"--target", strconv.FormatUint(uint64(nodeID), 10),
		"--format", "s16",
		"--rate", strconv.Itoa(rate),
		"--channels", strconv.Itoa(ch),
		"-",
	}
	cmd := exec.Command("pw-record", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return fmt.Errorf("pw-record stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = session.Close()
		return fmt.Errorf("pw-record start failed: %w", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.stdout = stdout
	s.mu.Unlock()

	log.Printf("[audio] portal node=%d via pw-record", nodeID)
	go s.readLoop(runCtx)
	go s.waitLoop()
	return nil
}

func (s *linuxPortalPWRecordSource) Frames() <-chan []byte {
	return s.frames
}

func (s *linuxPortalPWRecordSource) Close() error {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Lock()
		cmd := s.cmd
		stdout := s.stdout
		s.cmd = nil
		s.stdout = nil
		s.mu.Unlock()

		if stdout != nil {
			_ = stdout.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		if s.session != nil {
			_ = s.session.Close()
			s.session = nil
		}
	})
	return nil
}

func (s *linuxPortalPWRecordSource) readLoop(ctx context.Context) {
	defer close(s.frames)

	buf := make([]byte, s.frameSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := io.ReadFull(s.stdout, buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			return
		}
		if n != s.frameSize {
			continue
		}

		frame := make([]byte, s.frameSize)
		copy(frame, buf)
		select {
		case s.frames <- frame:
		default:
			select {
			case <-s.frames:
			default:
			}
			select {
			case s.frames <- frame:
			default:
			}
		}
	}
}

func (s *linuxPortalPWRecordSource) waitLoop() {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil {
		_ = cmd.Wait()
	}
}

func pickPortalAudioNode(streams []portal.StreamInfo) (uint32, bool) {
	for _, st := range streams {
		if streamLooksAudio(st.Properties) {
			return st.NodeID, true
		}
	}
	if len(streams) > 0 {
		return streams[len(streams)-1].NodeID, true
	}
	return 0, false
}

func streamLooksAudio(props map[string]dbus.Variant) bool {
	for k, v := range props {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "audio") {
			return true
		}
		s := strings.ToLower(fmt.Sprint(v.Value()))
		if strings.Contains(s, "audio") && !strings.Contains(s, "video") {
			return true
		}
	}
	return false
}
