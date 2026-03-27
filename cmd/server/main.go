package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"streamscreen/internal/config"
	"streamscreen/internal/video/capture"
	"streamscreen/internal/video/platform"
	"streamscreen/internal/video/stream/server"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := config.LoadServer("server.config.json")
	if err != nil {
		log.Fatalf("load server config: %v", err)
	}

	backend, err := platform.PrepareBackend(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := platform.ValidateBackendRuntime(backend); err != nil {
		log.Fatal(err)
	}

	destinationHost, err := cfg.DestinationHost()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("loaded server.config.json")
	log.Printf("backend=%s", backend)
	log.Printf("bind=%s:%d destination=%s:%d", cfg.BindHost, cfg.Port, destinationHost, cfg.Port)
	log.Printf("stream codec=%s capture=%dx%d@%dfps", cfg.StreamCodec, cfg.Capture.Width, cfg.Capture.Height, cfg.Capture.FPS)
	log.Printf("audio enabled=%t codec=%s sample_rate=%d channels=%d frame_ms=%d bitrate=%dkbps",
		cfg.Audio.Enabled, cfg.Audio.Codec, cfg.Audio.SampleRate, cfg.Audio.Channels, cfg.Audio.FrameMS, cfg.Audio.BitrateKbps)

	sender, err := server.NewSender(cfg, destinationHost)
	if err != nil {
		log.Fatalf("create server sender: %v", err)
	}
	defer sender.Stop()
	if err := sender.EnsureH264Pipeline(); err != nil {
		log.Fatalf("h264 init failed: %v", err)
	}
	sender.StartControlPlane()
	if err := sender.StartAudio(); err != nil {
		log.Fatalf("audio init failed: %v", err)
	}

	source, err := capture.New(cfg, backend)
	if err != nil {
		log.Fatalf("create capture source: %v", err)
	}
	defer source.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := source.Start(ctx); err != nil {
		log.Fatalf("start capture source: %v", err)
	}

	log.Printf("internal custom protocol stream started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(time.Duration(cfg.StatsIntervalMS) * time.Millisecond)
	defer ticker.Stop()

	var capturedFrames uint64
	lastCapturedFrames := uint64(0)
	lastStatsAt := time.Now()

	for {
		select {
		case sig := <-sigCh:
			log.Printf("received signal: %s", sig)
			return
		case frame, ok := <-source.Frames():
			if !ok {
				log.Printf("capture source stopped")
				return
			}
			sender.ProcessRGBAFrame(frame)
			capturedFrames++
		case <-ticker.C:
			now := time.Now()
			elapsed := now.Sub(lastStatsAt).Seconds()
			if elapsed <= 0 {
				elapsed = 1
			}
			capFPS := float64(capturedFrames-lastCapturedFrames) / elapsed
			lastCapturedFrames = capturedFrames
			lastStatsAt = now
			log.Printf("capture stats frames=%d fps=%.1f", capturedFrames, capFPS)
		}
	}
}

func init() {
	log.SetPrefix("[server] ")
}
