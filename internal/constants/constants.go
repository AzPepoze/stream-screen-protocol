package constants

import "time"

// Codec default settings
const (
	DefaultH264Preset   = "medium" // encoding speed (ultrafast, superfast, veryfast, faster, fast, medium)
	DefaultH264Bitrate  = 5000     // bitrate in kbps for H264 encoding
	DefaultCodec        = "rgba"   // default codec (RGBA pass-through)
)

// Client buffer configuration
const (
	NackRetryDelay              = 20 * time.Millisecond
	PartialFrameReadyThreshold  = 0.98  // 98% of packets
	LossTolerance               = 0.02  // 2% loss
	MaxLatency                  = 200 * time.Millisecond
)

// Network configuration constants
const (
	CSPHeaderSize   = 16   // custom streaming protocol header size in bytes
	MaxPacketSize   = 1316 // maximum UDP packet payload size
)

// Tile configuration constants
const (
	RequestDebounce       = 1 * time.Second
	StaleTimeout          = 5 * time.Second
	StaleCheckInterval    = 500 * time.Millisecond
	ForceAllTilesTimeout  = 2 * time.Second
	DefaultTileGridSize   = 10 // default grid size (NxN tiles)
)
