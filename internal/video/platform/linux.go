//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"streamscreen/internal/config"
)

func prepareBackend(cfg config.ServerConfig) (config.CaptureBackend, error) {
	backend, err := cfg.EffectiveBackend()
	if err != nil {
		return "", err
	}
	if backend != config.CaptureBackendPortalPipewire {
		return "", fmt.Errorf("linux internal capture currently supports only %q backend", config.CaptureBackendPortalPipewire)
	}
	return backend, nil
}

func validateBackendRuntime(backend config.CaptureBackend) error {
	if backend != config.CaptureBackendPortalPipewire {
		return fmt.Errorf("unsupported linux backend: %q", backend)
	}
	if _, err := exec.LookPath("gst-launch-1.0"); err != nil {
		return errors.New("gst-launch-1.0 was not found in PATH")
	}
	if _, err := exec.LookPath("gst-inspect-1.0"); err != nil {
		return errors.New("gst-inspect-1.0 was not found in PATH")
	}
	requiredLinuxElements := []string{
		"pipewiresrc",
		"videoconvert",
		"x264enc",
		"mpegtsmux",
		"udpsink",
	}
	for _, name := range requiredLinuxElements {
		if !elementAvailable(name) {
			return errors.New(missingElementHelp(name))
		}
	}
	return nil
}

func elementAvailable(name string) bool {
	cmd := exec.Command("gst-inspect-1.0", name)
	cmd.Env = os.Environ()
	return cmd.Run() == nil
}

func missingElementHelp(name string) string {
	switch name {
	case "udpsink":
		return "GStreamer element udpsink is missing. Install the GStreamer UDP/network plugins for your distro."
	case "pipewiresrc":
		return "GStreamer element pipewiresrc is missing. Install the PipeWire GStreamer plugin package."
	case "x264enc":
		return "GStreamer element x264enc is missing. Install the x264 GStreamer plugin package."
	case "mpegtsmux":
		return "GStreamer element mpegtsmux is missing. Install the GStreamer MPEG-TS plugin package."
	default:
		return fmt.Sprintf("required GStreamer element %s is missing", name)
	}
}
