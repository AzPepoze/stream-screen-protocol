package main

import (
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"

	"streamscreen/internal/config"
	"streamscreen/internal/stream/client"
)

type streamState string

const (
	stateWaiting   streamState = "waiting"
	stateStreaming streamState = "streaming"
	stateEnded     streamState = "ended"
	stateError     streamState = "error"
)

type sharedFrame struct {
	mu             sync.RWMutex
	pixels         []byte
	seq            uint64
	lastFrameAt    time.Time
	receivedFrames uint64
	receivedBytes  uint64
	state          streamState
}

type game struct {
	cfg                config.ClientConfig
	frame              *sharedFrame
	img                *ebiten.Image
	lastSeq            uint64
	lastReceivedFrames uint64
	lastBytes          uint64
	lastStatsAt        time.Time
	receiver           *client.ClientReceiver
	renderFrames       uint64
	incomingFPS        float64
	renderFPS          float64
	bitrateKbps        float64
	droppedBestEffort  uint64
	lastImgWidth       int
	lastImgHeight      int
	overlayImg         *ebiten.Image
	overlayImgW        int
	overlayImgH        int
	overlayText        string
	targetTPS          int
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := config.LoadClient("client.config.json")
	if err != nil {
		log.Fatalf("load client config: %v", err)
	}
	log.Printf("loaded client.config.json")

	log.Printf("starting internal custom protocol receiver")
	receiver, err := client.NewClientReceiver(cfg)
	if err != nil {
		log.Fatalf("create client receiver: %v", err)
	}
	log.Printf("calling receiver.Start() - will block until server VideoInfo received")
	if err := receiver.Start(); err != nil {
		log.Fatalf("start client receiver: %v", err)
	}
	log.Printf("receiver.Start() RETURNED successfully")

	frame := &sharedFrame{
		state: stateStreaming,
	}

	// Use server-provided resolution, not config defaults
	w, h := receiver.GetVideoResolution()
	log.Printf("receiver.GetVideoResolution() returned: %d x %d", w, h)
	windowWidth := int(w)
	windowHeight := int(h)
	if windowWidth <= 0 || windowHeight <= 0 {
		// Fallback to config if for some reason server info unavailable
		windowWidth = cfg.Window.Width
		windowHeight = cfg.Window.Height
		log.Printf("Resolution was <= 0, using config fallback: %d x %d", windowWidth, windowHeight)
	} else {
		log.Printf("Using server resolution: %d x %d", windowWidth, windowHeight)
	}

	ebiten.SetWindowTitle(cfg.Window.Title)
	ebiten.SetWindowSize(windowWidth, windowHeight)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if cfg.Window.Fullscreen {
		ebiten.SetFullscreen(true)
	}
	videoFPS := int(receiver.GetVideoFPS())
	if videoFPS <= 0 {
		videoFPS = 60
	}
	ebiten.SetTPS(videoFPS)

	g := &game{
		cfg:           cfg,
		frame:         frame,
		receiver:      receiver,
		img:           ebiten.NewImage(windowWidth, windowHeight),
		lastStatsAt:   time.Now(),
		lastImgWidth:  windowWidth,
		lastImgHeight: windowHeight,
		targetTPS:     videoFPS,
	}
	log.Printf("[canvas] Created initial canvas: %dx%d", windowWidth, windowHeight)

	err = ebiten.RunGame(g)
	_ = receiver.Stop()
	if err != nil {
		log.Fatal(err)
	}
}

func (g *game) Update() error {
	g.renderFrames++
	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		return ebiten.Termination
	}

	// Ensure window size is correct (force it every frame to override WM constraints)
	w, h := g.receiver.GetVideoResolution()
	fps := int(g.receiver.GetVideoFPS())
	actualW, actualH := ebiten.WindowSize()
	targetW, targetH := int(w), int(h)

	if g.renderFrames == 1 || g.renderFrames%60 == 0 {
		log.Printf("[UPDATE] Frame=%d: Actual=%dx%d, Target=%dx%d, LastImg=%dx%d",
			g.renderFrames, actualW, actualH, targetW, targetH, g.lastImgWidth, g.lastImgHeight)
	}

	// If receiver has resolution but window is wrong size, fix it
	if targetW > 0 && targetH > 0 && (actualW != targetW || actualH != targetH) {
		log.Printf("[UPDATE] Window size mismatch! Setting to %dx%d (was %dx%d)", targetW, targetH, actualW, actualH)
		ebiten.SetWindowSize(targetW, targetH)
	}
	if fps > 0 && fps != g.targetTPS {
		ebiten.SetTPS(fps)
		g.targetTPS = fps
	}

	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	pixels, seq := g.receiver.Pixels()

	// Get current server resolution from receiver
	w, h := g.receiver.GetVideoResolution()

	// Recreate image and resize window if resolution changed
	if (w > 0 && h > 0) && (g.lastImgWidth != int(w) || g.lastImgHeight != int(h)) {
		g.img = ebiten.NewImage(int(w), int(h))
		g.lastImgWidth = int(w)
		g.lastImgHeight = int(h)
		// Also resize the window to match
		ebiten.SetWindowSize(int(w), int(h))
		log.Printf("[canvas] Updated canvas size to %dx%d", w, h)
	}

	if seq != g.lastSeq && len(pixels) > 0 {
		// Only write pixels if image is correctly sized
		expectedSize := g.lastImgWidth * g.lastImgHeight * 4
		if g.img != nil && len(pixels) == expectedSize {
			g.img.WritePixels(pixels)
			deltaFrames := uint64(1)
			if seq > g.lastSeq {
				deltaFrames = seq - g.lastSeq
			}
			g.lastSeq = seq
			g.frame.mu.Lock()
			g.frame.receivedFrames += deltaFrames
			g.frame.receivedBytes += uint64(len(pixels)) * deltaFrames
			g.frame.lastFrameAt = time.Now()
			g.frame.mu.Unlock()
		} else if len(pixels) != expectedSize {
			log.Printf("[draw] Pixel size mismatch: got %d bytes, expected %d (%dx%d*4)", len(pixels), expectedSize, g.lastImgWidth, g.lastImgHeight)
		}
	}

	g.frame.mu.RLock()
	receivedFrames := g.frame.receivedFrames
	receivedBytes := g.frame.receivedBytes
	lastFrameAt := g.frame.lastFrameAt
	state := g.frame.state
	g.frame.mu.RUnlock()

	screen.DrawImage(g.img, nil)

	now := time.Now()
	elapsed := now.Sub(g.lastStatsAt)
	if elapsed >= time.Duration(g.cfg.Stats.UpdateIntervalMS)*time.Millisecond {
		seconds := elapsed.Seconds()
		g.incomingFPS = float64(receivedFrames-g.lastReceivedFrames) / seconds
		g.renderFPS = float64(g.renderFrames) / seconds
		g.bitrateKbps = (float64(receivedBytes-g.lastBytes) * 8 / seconds) / 1000
		g.lastReceivedFrames = receivedFrames
		g.lastBytes = receivedBytes
		g.renderFrames = 0
		g.lastStatsAt = now
	}

	if g.cfg.Stats.Debug {
		latencyMS := int64(0)
		if !lastFrameAt.IsZero() {
			latencyMS = time.Since(lastFrameAt).Milliseconds()
		}
		w, h := g.receiver.GetVideoResolution()
		actualW, actualH := ebiten.WindowSize()
		overlay := "" +
			"STATE: " + string(state) + "\n" +
			"SERVER: " + g.cfg.ServerHost + ":" + itoa(g.cfg.Port) + "\n" +
			"RES: " + itoa(int(w)) + "x" + itoa(int(h)) + " (window: " + itoa(actualW) + "x" + itoa(actualH) + ")\n" +
			"IN FPS: " + formatFloat(g.incomingFPS) + "\n" +
			"RENDER FPS: " + formatFloat(g.renderFPS) + "\n" +
			"BITRATE: " + formatFloat(g.bitrateKbps) + " kbps\n" +
			"LATENCY: " + itoa64(latencyMS) + " ms\n" +
			"RX FRAMES: " + itoa64(int64(receivedFrames))
		g.overlayText = overlay
	} else {
		g.overlayText = ""
	}
}

func (g *game) Layout(_, _ int) (int, int) {
	// Return the actual video resolution from server, not config defaults
	w, h := g.receiver.GetVideoResolution()
	if w > 0 && h > 0 {
		return int(w), int(h)
	}
	// Fallback to config if no video info yet
	return g.cfg.Window.Width, g.cfg.Window.Height
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}

func (g *game) DrawFinalScreen(screen ebiten.FinalScreen, offscreen *ebiten.Image, geoM ebiten.GeoM) {
	ebiten.DefaultDrawFinalScreen(screen, offscreen, geoM)

	if g.overlayText == "" {
		return
	}

	lines := strings.Split(g.overlayText, "\n")
	maxChars := 0
	for _, line := range lines {
		if len(line) > maxChars {
			maxChars = len(line)
		}
	}

	// DebugPrint uses a fixed-size bitmap font (~6x16 per glyph).
	imgW := maxChars*6 + 8
	imgH := len(lines)*16 + 8
	if imgW <= 0 || imgH <= 0 {
		return
	}

	if g.overlayImg == nil || g.overlayImgW != imgW || g.overlayImgH != imgH {
		g.overlayImg = ebiten.NewImage(imgW, imgH)
		g.overlayImgW = imgW
		g.overlayImgH = imgH
	}
	g.overlayImg.Clear()
	ebitenutil.DebugPrint(g.overlayImg, g.overlayText)

	// Keep fixed text size independent of window dimensions.
	fontSize := g.cfg.Stats.FontSize
	if fontSize <= 0 {
		fontSize = 16
	}
	scale := float64(fontSize) / 16.0
	if scale < 0.5 {
		scale = 0.5
	}
	if scale > 6 {
		scale = 6
	}

	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(scale, scale)
	op.GeoM.Translate(10, 10)
	screen.DrawImage(g.overlayImg, op)
}

func init() {
	log.SetPrefix("[client] ")
}
