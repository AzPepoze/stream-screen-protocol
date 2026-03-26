package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"

	"streamscreen/internal/constants"
)

type CaptureBackend string

const (
	CaptureBackendAuto           CaptureBackend = "auto"
	CaptureBackendPortalPipewire CaptureBackend = "portal-pipewire"
	CaptureBackendDDAGrab        CaptureBackend = "ddagrab"
	CaptureBackendGDIGrab        CaptureBackend = "gdigrab"
)

type ServerConfig struct {
	BindHost   string `json:"bind_host"`
	ClientHost string `json:"client_host"`
	Port       int    `json:"port"`
	Capture    struct {
		Backend    CaptureBackend `json:"backend"`
		FPS        int            `json:"fps"`
		Width      int            `json:"width"`
		Height     int            `json:"height"`
		Source     string         `json:"source"`
		CursorMode string         `json:"cursor_mode"`
		SourceType string         `json:"source_type"`
	} `json:"capture"`
	Video struct {
		Codec        string `json:"codec"`
		BitrateKbps  int    `json:"bitrate_kbps"`
		Preset       string `json:"preset"`
		Tune         string `json:"tune"`
		TileGridSize int    `json:"tile_grid_size"`
	} `json:"video"`
	StreamCodec     string                 `json:"stream_codec"` // Transmission codec: "rgba" or "h264"
	CodecConfig     map[string]interface{} `json:"codec_config"` // Codec-specific settings
	StatsIntervalMS int                    `json:"stats_interval_ms"`
}

type ClientConfig struct {
	ServerHost  string                 `json:"server_host"`
	Port        int                    `json:"port"`
	FPS         int                    `json:"fps"`
	CodecConfig map[string]interface{} `json:"codec_config"`
	Window      struct {
		Title      string `json:"title"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		Fullscreen bool   `json:"fullscreen"`
	} `json:"window"`
	Stats struct {
		Debug            bool `json:"debug"`
		FontSize         int  `json:"font_size"`
		UpdateIntervalMS int  `json:"update_interval_ms"`
	} `json:"stats"`
	Network struct {
		MaxLatencyMS      int     `json:"max_latency_ms"`
		NackRetryMS       int     `json:"nack_retry_ms"`
		PartialFrameReady float64 `json:"partial_frame_ready"`
		AllowPartial      bool    `json:"allow_partial_frames"`
		ForceOutput       bool    `json:"force_output_partial"`
		AutoTuneByFPS     bool    `json:"auto_tune_by_fps"`
	} `json:"network"`
}

func LoadServer(path string) (ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ServerConfig{}, err
	}

	var cfg ServerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ServerConfig{}, err
	}

	var compat serverConfigCompat
	if err := json.Unmarshal(data, &compat); err != nil {
		return ServerConfig{}, err
	}
	applyServerCompat(&cfg, compat)

	return cfg, cfg.Validate()
}

func LoadClient(path string) (ClientConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ClientConfig{}, err
	}

	var cfg ClientConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ClientConfig{}, err
	}

	var compat clientConfigCompat
	if err := json.Unmarshal(data, &compat); err != nil {
		return ClientConfig{}, err
	}
	applyClientCompat(&cfg, compat)

	return cfg, cfg.Validate()
}

func (c ServerConfig) Validate() error {
	if c.BindHost == "" {
		return errors.New("bind_host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if c.Capture.FPS <= 0 {
		return errors.New("capture.fps must be greater than 0")
	}
	if c.Capture.Width <= 0 || c.Capture.Height <= 0 {
		return errors.New("capture.width and capture.height must be greater than 0")
	}
	if c.Capture.Backend == "" {
		return errors.New("capture.backend is required")
	}
	switch c.Capture.CursorMode {
	case "", "hidden", "embedded", "metadata":
	default:
		return errors.New("capture.cursor_mode must be one of hidden, embedded, metadata")
	}
	switch c.Capture.SourceType {
	case "", "monitor", "window", "virtual", "any":
	default:
		return errors.New("capture.source_type must be one of monitor, window, virtual, any")
	}
	if c.Video.Codec == "" {
		return errors.New("video.codec is required")
	}
	if c.Video.BitrateKbps <= 0 {
		return errors.New("video.bitrate_kbps must be greater than 0")
	}
	if c.Video.Preset == "" || c.Video.Tune == "" {
		return errors.New("video.preset and video.tune are required")
	}
	if c.StatsIntervalMS <= 0 {
		return errors.New("stats_interval_ms must be greater than 0")
	}
	if err := validateH264BackendValue(c.CodecConfig, "h264_encoder_backend"); err != nil {
		return err
	}
	if err := validateH264BackendValue(c.CodecConfig, "h264_decoder_backend"); err != nil {
		return err
	}
	return nil
}

func (c ClientConfig) Validate() error {
	if c.ServerHost == "" {
		return errors.New("server_host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if c.Window.Title == "" {
		return errors.New("window.title is required")
	}
	if c.Window.Width <= 0 || c.Window.Height <= 0 {
		return errors.New("window.width and window.height must be greater than 0")
	}
	if c.Stats.UpdateIntervalMS <= 0 {
		return errors.New("stats.update_interval_ms must be greater than 0")
	}
	if c.Network.MaxLatencyMS <= 0 {
		return errors.New("network.max_latency_ms must be greater than 0")
	}
	if c.Network.NackRetryMS <= 0 {
		return errors.New("network.nack_retry_ms must be greater than 0")
	}
	if c.Network.PartialFrameReady <= 0 || c.Network.PartialFrameReady > 1 {
		return errors.New("network.partial_frame_ready must be in (0, 1]")
	}
	if err := validateH264BackendValue(c.CodecConfig, "h264_decoder_backend"); err != nil {
		return err
	}
	return nil
}

func validateH264BackendValue(cfg map[string]interface{}, key string) error {
	if cfg == nil {
		return nil
	}
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	v, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", key)
	}
	switch v {
	case "", "auto", "gstreamer", "ffmpeg":
		return nil
	default:
		return fmt.Errorf("%s must be one of auto, gstreamer, ffmpeg", key)
	}
}

func (c ServerConfig) EffectiveBackend() (CaptureBackend, error) {
	if c.Capture.Backend != "" && c.Capture.Backend != CaptureBackendAuto {
		return c.Capture.Backend, nil
	}

	switch runtime.GOOS {
	case "linux":
		return CaptureBackendPortalPipewire, nil
	case "windows":
		return CaptureBackendDDAGrab, nil
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func (c ServerConfig) DestinationHost() (string, error) {
	if c.ClientHost != "" {
		return c.ClientHost, nil
	}
	if c.BindHost != "0.0.0.0" && c.BindHost != "::" {
		return c.BindHost, nil
	}
	return "", errors.New("client_host is required when bind_host is wildcard")
}

func load(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

type serverConfigCompat struct {
	Codec           string                 `json:"codec"`
	Bitrate         int                    `json:"bitrate"`
	RGBACodecConfig map[string]interface{} `json:"rgba_codec_config"`
	H264CodecConfig map[string]interface{} `json:"h264_codec_config"`
}

type clientConfigCompat struct {
	Stats struct {
		Debug       *bool `json:"debug"`
		ShowOverlay *bool `json:"show_overlay"`
	} `json:"stats"`
	Network struct {
		AllowPartial  *bool `json:"allow_partial_frames"`
		ForceOutput   *bool `json:"force_output_partial"`
		AutoTuneByFPS *bool `json:"auto_tune_by_fps"`
	} `json:"network"`
}

func applyClientCompat(cfg *ClientConfig, compat clientConfigCompat) {
	if cfg.CodecConfig == nil {
		cfg.CodecConfig = make(map[string]interface{})
	}
	if cfg.Stats.FontSize <= 0 {
		cfg.Stats.FontSize = 16
	}
	if compat.Stats.Debug != nil {
		cfg.Stats.Debug = *compat.Stats.Debug
	} else if compat.Stats.ShowOverlay != nil {
		cfg.Stats.Debug = *compat.Stats.ShowOverlay
	} else {
		cfg.Stats.Debug = true
	}

	if cfg.Network.MaxLatencyMS <= 0 {
		cfg.Network.MaxLatencyMS = 200
	}
	if cfg.Network.NackRetryMS <= 0 {
		cfg.Network.NackRetryMS = 20
	}
	if cfg.Network.PartialFrameReady <= 0 || cfg.Network.PartialFrameReady > 1 {
		cfg.Network.PartialFrameReady = 0.98
	}
	if compat.Network.AllowPartial != nil {
		cfg.Network.AllowPartial = *compat.Network.AllowPartial
	} else {
		cfg.Network.AllowPartial = true
	}
	if compat.Network.ForceOutput != nil {
		cfg.Network.ForceOutput = *compat.Network.ForceOutput
	} else {
		cfg.Network.ForceOutput = true
	}
	if compat.Network.AutoTuneByFPS != nil {
		cfg.Network.AutoTuneByFPS = *compat.Network.AutoTuneByFPS
	} else {
		cfg.Network.AutoTuneByFPS = true
	}
}

func applyServerCompat(cfg *ServerConfig, compat serverConfigCompat) {
	if cfg.StreamCodec == "" && compat.Codec != "" {
		cfg.StreamCodec = compat.Codec
	}
	if cfg.StreamCodec == "" {
		cfg.StreamCodec = constants.DefaultCodec
	}

	if cfg.CodecConfig == nil {
		cfg.CodecConfig = make(map[string]interface{})
	}
	switch cfg.StreamCodec {
	case "h264":
		mergeMapIfMissing(cfg.CodecConfig, compat.H264CodecConfig)
	case "rgba":
		mergeMapIfMissing(cfg.CodecConfig, compat.RGBACodecConfig)
	}

	// Accept common alias keys in h264_codec_config.
	if v, ok := cfg.CodecConfig["bitrate_kbps"]; ok {
		if _, exists := cfg.CodecConfig["bitrate"]; !exists {
			cfg.CodecConfig["bitrate"] = v
		}
	}
	if v, ok := cfg.CodecConfig["speed_preset"]; ok {
		if _, exists := cfg.CodecConfig["preset"]; !exists {
			cfg.CodecConfig["preset"] = v
		}
	}
	if v, ok := cfg.CodecConfig["key_int_max"]; ok {
		if _, exists := cfg.CodecConfig["key-int-max"]; !exists {
			cfg.CodecConfig["key-int-max"] = v
		}
	}
	if v, ok := cfg.CodecConfig["tile_size"]; ok {
		if _, exists := cfg.CodecConfig["tile_num"]; !exists {
			cfg.CodecConfig["tile_num"] = v
		}
	}
	if v, ok := cfg.CodecConfig["tile_num"]; ok {
		if _, exists := cfg.CodecConfig["tile_size"]; !exists {
			cfg.CodecConfig["tile_size"] = v
		}
	}

	if cfg.Video.BitrateKbps <= 0 {
		switch {
		case compat.Bitrate > 0:
			cfg.Video.BitrateKbps = compat.Bitrate
		case intFromAny(cfg.CodecConfig["bitrate"]) > 0:
			cfg.Video.BitrateKbps = intFromAny(cfg.CodecConfig["bitrate"])
		case intFromAny(cfg.CodecConfig["bitrate_kbps"]) > 0:
			cfg.Video.BitrateKbps = intFromAny(cfg.CodecConfig["bitrate_kbps"])
		default:
			cfg.Video.BitrateKbps = constants.DefaultH264Bitrate
		}
	}
	if cfg.Video.Codec == "" {
		cfg.Video.Codec = "libx264"
	}
	if cfg.Video.Preset == "" {
		if preset := stringFromAny(cfg.CodecConfig["preset"]); preset != "" {
			cfg.Video.Preset = preset
		} else if preset := stringFromAny(cfg.CodecConfig["speed_preset"]); preset != "" {
			cfg.Video.Preset = preset
		} else {
			cfg.Video.Preset = constants.DefaultH264Preset
		}
	}
	if cfg.Video.Tune == "" {
		if tune := stringFromAny(cfg.CodecConfig["tune"]); tune != "" {
			cfg.Video.Tune = tune
		} else {
			cfg.Video.Tune = "zerolatency"
		}
	}
	if cfg.Video.TileGridSize <= 0 {
		if tileSize := intFromAny(cfg.CodecConfig["tile_size"]); tileSize > 0 {
			cfg.Video.TileGridSize = tileSize
		} else if tileSize := intFromAny(cfg.CodecConfig["tile_num"]); tileSize > 0 {
			cfg.Video.TileGridSize = tileSize
		} else {
			cfg.Video.TileGridSize = constants.DefaultTileGridSize
		}
	}
	if cfg.StatsIntervalMS <= 0 {
		cfg.StatsIntervalMS = 1000
	}
}

func mergeMapIfMissing(dst map[string]interface{}, src map[string]interface{}) {
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
}

func intFromAny(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case float32:
		return int(t)
	default:
		return 0
	}
}

func stringFromAny(v interface{}) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
