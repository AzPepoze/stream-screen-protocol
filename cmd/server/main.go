package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"streamscreen/internal/config"
	"streamscreen/internal/logger"
	"streamscreen/internal/video/capture"
	"streamscreen/internal/video/platform"
	"streamscreen/internal/video/stream/server"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var cfgPath string
	flag.StringVar(&cfgPath, "config", "server.config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.LoadServer(cfgPath)
	if err != nil {
		logger.Error("server", "%v", err)
	}

	backend, err := platform.PrepareBackend(cfg)
	if err != nil {
		logger.Error("server", "%v", err)
	}
	if err := platform.ValidateBackendRuntime(backend); err != nil {
		logger.Error("server", "%v", err)
	}

	logger.Info("server", "loaded %s", cfgPath)
	logger.Info("server", "backend=%s", backend)
	logger.Info("server", "bind=%s:%d (waiting for clients)", cfg.BindHost, cfg.Port)
	logger.Info("server", "stream codec=%s capture=%dx%d@%dfps", cfg.Capture.Codec, cfg.Capture.Width, cfg.Capture.Height, cfg.Capture.FPS)
	logger.Info("server", "audio enabled=%t codec=%s sample_rate=%d channels=%d frame_ms=%d bitrate=%dkbps",
		cfg.Audio.Enabled, cfg.Audio.Codec, cfg.Audio.SampleRate, cfg.Audio.Channels, cfg.Audio.FrameMS, cfg.Audio.BitrateKbps)

	sender, err := server.NewSender(cfg)
	if err != nil {
		logger.Error("server", "create server sender: %v", err)
	}
	defer func() { _ = sender.Stop() }()
	if err := sender.EnsureH264Pipeline(); err != nil {
		logger.Error("server", "h264 init failed: %v", err)
	}
	sender.StartControlPlane()
	if err := sender.StartAudio(); err != nil {
		logger.Error("server", "audio init failed: %v", err)
	}

	source, err := capture.New(cfg, backend)
	if err != nil {
		logger.Error("server", "create capture source: %v", err)
	}
	defer func() { _ = source.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := source.Start(ctx); err != nil {
		logger.Error("server", "start capture source: %v", err)
	}

	logger.Info("server", "internal custom protocol stream started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	statsInterval := cfg.StatsIntervalMS
	if statsInterval <= 0 {
		statsInterval = 1000
	}
	ticker := time.NewTicker(time.Duration(statsInterval) * time.Millisecond)
	defer ticker.Stop()

	configWatchTicker := time.NewTicker(500 * time.Millisecond)
	defer configWatchTicker.Stop()

	var lastCfgModTime time.Time
	if fi, err := os.Stat(cfgPath); err == nil {
		lastCfgModTime = fi.ModTime()
	}

	var capturedFrames uint64
	lastCapturedFrames := uint64(0)
	lastStatsAt := time.Now()

	for {
		select {
		case sig := <-sigCh:
			logger.Info("server", "received signal: %s", sig)
			return
		case frame, ok := <-source.Frames():
			if !ok {
				logger.Info("server", "capture source stopped")
				return
			}
			sender.ProcessRGBAFrame(frame)
			capturedFrames++
		case <-configWatchTicker.C:
			fi, err := os.Stat(cfgPath)
			if err == nil && fi.ModTime().After(lastCfgModTime) {
				lastCfgModTime = fi.ModTime()
				newCfg, err := config.LoadServer(cfgPath)
				if err == nil {
					logger.Info("config", "detected change in %s, applying video updates", cfgPath)
					_ = sender.UpdateVideoConfig(newCfg)
					cfg = newCfg
				} else {
					logger.Info("config", "error reloading %s: %v", cfgPath, err)
				}
			}
		case <-ticker.C:
			now := time.Now()
			elapsed := now.Sub(lastStatsAt).Seconds()
			if elapsed <= 0 {
				elapsed = 1
			}
			capFPS := float64(capturedFrames-lastCapturedFrames) / elapsed
			lastCapturedFrames = capturedFrames
			lastStatsAt = now
			logger.Info("server", "capture stats frames=%d fps=%.1f", capturedFrames, capFPS)
		}
	}
}

func init() {
	log.SetPrefix("[server] ")
}
