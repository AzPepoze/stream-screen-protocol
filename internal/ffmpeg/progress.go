package ffmpeg

import (
	"bufio"
	"io"
	"strings"
	"sync"
	"time"
)

type Progress struct {
	mu           sync.RWMutex
	Frame        string
	FPS          string
	Bitrate      string
	DropFrames   string
	LastStatus   string
	LastUpdate   time.Time
	WarningCount int
	LastWarning  string
}

func (p *Progress) Snapshot() Progress {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return Progress{
		Frame:        p.Frame,
		FPS:          p.FPS,
		Bitrate:      p.Bitrate,
		DropFrames:   p.DropFrames,
		LastStatus:   p.LastStatus,
		LastUpdate:   p.LastUpdate,
		WarningCount: p.WarningCount,
		LastWarning:  p.LastWarning,
	}
}

func (p *Progress) Consume(r io.Reader, onWarning func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if i := strings.IndexByte(line, '='); i >= 0 {
			key := line[:i]
			value := line[i+1:]
			p.mu.Lock()
			switch key {
			case "frame":
				p.Frame = value
			case "fps":
				p.FPS = value
			case "bitrate":
				p.Bitrate = value
			case "drop_frames":
				p.DropFrames = value
			case "progress":
				p.LastStatus = value
			}
			p.LastUpdate = time.Now()
			p.mu.Unlock()
			continue
		}

		p.mu.Lock()
		p.WarningCount++
		p.LastWarning = line
		p.LastUpdate = time.Now()
		p.mu.Unlock()
		if onWarning != nil {
			onWarning(line)
		}
	}
}
