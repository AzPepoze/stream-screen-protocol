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
		Backend         CaptureBackend         `json:"backend"`
		FPS             int                    `json:"fps"`
		Width           int                    `json:"width"`
		Height          int                    `json:"height"`
		Source          string                 `json:"source"`
		CursorMode      string                 `json:"cursor_mode"`
		SourceType      string                 `json:"source_type"`
		Codec           string                 `json:"codec"`
		RGBACodecConfig map[string]interface{} `json:"rgba_codec_config"`
		H264CodecConfig map[string]interface{} `json:"h264_codec_config"`
	} `json:"capture"`
	StatsIntervalMS int `json:"stats_interval_ms"`
	Audio           struct {
		Enabled     bool   `json:"enabled"`
		Codec       string `json:"codec"`
		SampleRate  int    `json:"sample_rate"`
		Channels    int    `json:"channels"`
		FrameMS     int    `json:"frame_ms"`
		BitrateKbps int    `json:"bitrate_kbps"`
		InputDevice string `json:"audio_input_device"`
	} `json:"audio"`
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
	Audio struct {
		Enabled      bool   `json:"enabled"`
		OutputDevice string `json:"audio_output_device"`
	} `json:"audio"`
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
	if c.Capture.Codec == "" {
		return errors.New("capture.codec is required")
	}
	if c.StatsIntervalMS <= 0 {
		return errors.New("stats_interval_ms must be greater than 0")
	}
	if err := validateH264BackendValue(c.Capture.H264CodecConfig, "h264_encoder_backend"); err != nil {
		return err
	}
	if err := validateH264BackendValue(c.Capture.H264CodecConfig, "h264_decoder_backend"); err != nil {
		return err
	}
	if c.Audio.Enabled {
		if c.Audio.Codec == "" {
			return errors.New("audio.codec is required when audio.enabled=true")
		}
		if c.Audio.Codec != "opus" {
			return errors.New("audio.codec must be opus")
		}
		if c.Audio.SampleRate <= 0 {
			return errors.New("audio.sample_rate must be greater than 0")
		}
		if c.Audio.Channels <= 0 {
			return errors.New("audio.channels must be greater than 0")
		}
		if c.Audio.FrameMS <= 0 {
			return errors.New("audio.frame_ms must be greater than 0")
		}
		if c.Audio.BitrateKbps <= 0 {
			return errors.New("audio.bitrate_kbps must be greater than 0")
		}
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
	if cfg.Audio.OutputDevice == "" {
		cfg.Audio.OutputDevice = "default"
	}
}

func applyServerCompat(cfg *ServerConfig, compat serverConfigCompat) {
	if cfg.Capture.Codec == "" && compat.Codec != "" {
		cfg.Capture.Codec = compat.Codec
	}
	if cfg.Capture.Codec == "" {
		cfg.Capture.Codec = constants.DefaultCodec
	}

	if cfg.Capture.H264CodecConfig == nil {
		cfg.Capture.H264CodecConfig = make(map[string]interface{})
	}
	if cfg.Capture.RGBACodecConfig == nil {
		cfg.Capture.RGBACodecConfig = make(map[string]interface{})
	}

	switch cfg.Capture.Codec {
	case "h264":
		mergeMapIfMissing(cfg.Capture.H264CodecConfig, compat.H264CodecConfig)
	case "rgba":
		mergeMapIfMissing(cfg.Capture.RGBACodecConfig, compat.RGBACodecConfig)
	}

	// Accept common alias keys in h264_codec_config.
	if v, ok := cfg.Capture.H264CodecConfig["bitrate_kbps"]; ok {
		if _, exists := cfg.Capture.H264CodecConfig["bitrate"]; !exists {
			cfg.Capture.H264CodecConfig["bitrate"] = v
		}
	}
	if v, ok := cfg.Capture.H264CodecConfig["speed_preset"]; ok {
		if _, exists := cfg.Capture.H264CodecConfig["preset"]; !exists {
			cfg.Capture.H264CodecConfig["preset"] = v
		}
	}
	if v, ok := cfg.Capture.H264CodecConfig["key_int_max"]; ok {
		if _, exists := cfg.Capture.H264CodecConfig["key-int-max"]; !exists {
			cfg.Capture.H264CodecConfig["key-int-max"] = v
		}
	}
	if v, ok := cfg.Capture.RGBACodecConfig["tile_size"]; ok {
		if _, exists := cfg.Capture.RGBACodecConfig["tile_num"]; !exists {
			cfg.Capture.RGBACodecConfig["tile_num"] = v
		}
	}
	if v, ok := cfg.Capture.RGBACodecConfig["tile_num"]; ok {
		if _, exists := cfg.Capture.RGBACodecConfig["tile_size"]; !exists {
			cfg.Capture.RGBACodecConfig["tile_size"] = v
		}
	}

	if cfg.StatsIntervalMS <= 0 {
		cfg.StatsIntervalMS = 1000
	}
	if cfg.Audio.Codec == "" {
		cfg.Audio.Codec = "opus"
	}
	if cfg.Audio.SampleRate <= 0 {
		cfg.Audio.SampleRate = 48000
	}
	if cfg.Audio.Channels <= 0 {
		cfg.Audio.Channels = 2
	}
	if cfg.Audio.FrameMS <= 0 {
		cfg.Audio.FrameMS = 20
	}
	if cfg.Audio.BitrateKbps <= 0 {
		cfg.Audio.BitrateKbps = 96
	}
	if cfg.Audio.InputDevice == "" {
		cfg.Audio.InputDevice = "default"
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
