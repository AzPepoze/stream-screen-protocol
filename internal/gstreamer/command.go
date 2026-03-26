package gstreamer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var requiredLinuxElements = []string{
	"pipewiresrc",
	"videoconvert",
	"x264enc",
	"mpegtsmux",
	"udpsink",
}

func ProbeGStreamer() error {
	if _, err := exec.LookPath("gst-launch-1.0"); err != nil {
		return errors.New("gst-launch-1.0 was not found in PATH")
	}
	if _, err := exec.LookPath("gst-inspect-1.0"); err != nil {
		return errors.New("gst-inspect-1.0 was not found in PATH")
	}
	return nil
}

func ElementAvailable(name string) bool {
	cmd := exec.Command("gst-inspect-1.0", name)
	cmd.Env = os.Environ()
	return cmd.Run() == nil
}

func MissingElementHelp(name string) string {
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

func EnsureLinuxPlugins() error {
	for _, name := range requiredLinuxElements {
		if !ElementAvailable(name) {
			return errors.New(MissingElementHelp(name))
		}
	}
	return nil
}

func Join(args []string) string {
	return strings.Join(args, " ")
}
