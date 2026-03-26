package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"streamscreen/internal/config"
	"streamscreen/internal/ffmpeg"
	"streamscreen/internal/gstreamer"
	"streamscreen/internal/portal"
	"streamscreen/internal/stream/server"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := config.LoadServer("server.config.json")
	if err != nil {
		log.Fatalf("load server config: %v", err)
	}

	backend, err := cfg.EffectiveBackend()
	if err != nil {
		log.Fatal(err)
	}

	if runtime.GOOS == "windows" && backend == config.CaptureBackendDDAGrab {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !ffmpeg.DdagrabAvailable(ctx, cfg.Capture.FPS) {
			log.Printf("ddagrab probe failed, falling back to gdigrab")
			backend = config.CaptureBackendGDIGrab
		}
	}

	if runtime.GOOS != "linux" || backend != config.CaptureBackendPortalPipewire {
		if err := ffmpeg.ProbeFFmpeg(); err != nil {
			log.Fatal(err)
		}
	}

	destinationHost, err := cfg.DestinationHost()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("loaded server.config.json")
	log.Printf("os=%s backend=%s", runtime.GOOS, backend)
	log.Printf("bind=%s:%d destination=%s:%d", cfg.BindHost, cfg.Port, destinationHost, cfg.Port)
	log.Printf("video codec=%s fps=%d bitrate=%dkbps preset=%s tune=%s",
		cfg.Video.Codec, cfg.Capture.FPS, cfg.Video.BitrateKbps, cfg.Video.Preset, cfg.Video.Tune)

	if runtime.GOOS == "linux" && backend == config.CaptureBackendPortalPipewire {
		runLinuxPortalServer(cfg, destinationHost)
		return
	}

	args := ffmpeg.ServerArgs(cfg, backend, destinationHost)
	log.Printf("ffmpeg command: ffmpeg %s", ffmpeg.Join(args))

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = os.Stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("stderr pipe: %v", err)
	}

	progress := &ffmpeg.Progress{}
	go progress.Consume(stderr, func(line string) {
		log.Printf("ffmpeg: %s", line)
	})

	if err := cmd.Start(); err != nil {
		log.Fatalf("start ffmpeg: %v", err)
	}
	log.Printf("stream started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	ticker := time.NewTicker(time.Duration(cfg.StatsIntervalMS) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case sig := <-sigCh:
			log.Printf("received signal: %s", sig)
			_ = terminate(cmd.Process)
		case <-ticker.C:
			s := progress.Snapshot()
			log.Printf("encode stats frame=%s fps=%s bitrate=%s dropped=%s status=%s",
				or(s.Frame, "?"),
				or(s.FPS, "?"),
				or(s.Bitrate, "?"),
				or(s.DropFrames, "0"),
				or(s.LastStatus, "running"),
			)
		case err := <-done:
			if err != nil {
				log.Fatalf("ffmpeg exited: %v", err)
			}
			log.Printf("stream stopped")
			return
		}
	}
}

func runLinuxPortalServer(cfg config.ServerConfig, destinationHost string) {
	if err := gstreamer.ProbeGStreamer(); err != nil {
		log.Fatal(err)
	}
	if err := gstreamer.EnsureLinuxPlugins(); err != nil {
		log.Fatal(err)
	}

	log.Printf("starting portal screencast session over D-Bus")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	session, err := portal.StartScreenCast(ctx, cfg)
	if err != nil {
		log.Fatalf("portal screencast failed: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			log.Printf("portal session cleanup: %v", err)
		}
	}()

	streamInfo := session.Streams[0]
	log.Printf("portal session ready stream_node=%d streams=%d", streamInfo.NodeID, len(session.Streams))

	sender, err := server.NewSender(cfg, destinationHost)
	if err != nil {
		log.Fatalf("create server sender: %v", err)
	}
	defer sender.Stop()

	if err := sender.Start(int(session.RemoteFile().Fd()), streamInfo.NodeID); err != nil {
		log.Fatalf("start server sender: %v", err)
	}
	log.Printf("internal custom protocol stream started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(time.Duration(cfg.StatsIntervalMS) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case sig := <-sigCh:
			log.Printf("received signal: %s", sig)
			return
		case <-ticker.C:
			log.Printf("pipeline status stream_node=%d running", streamInfo.NodeID)
		}
	}
}

type lineTracker struct {
	last atomicText
}

func (t *lineTracker) consume(r io.Reader, onLine func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		t.last.Store(line)
		if onLine != nil {
			onLine(line)
		}
	}
}

func (t *lineTracker) Last() string {
	return t.last.Load()
}

type atomicText struct {
	value atomic.Value
}

func (a *atomicText) Load() string {
	if v := a.value.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (a *atomicText) Store(v string) {
	a.value.Store(v)
}

func terminate(p *os.Process) error {
	if p == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	return p.Signal(syscall.SIGTERM)
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func init() {
	log.SetPrefix(fmt.Sprintf("[server] "))
}
