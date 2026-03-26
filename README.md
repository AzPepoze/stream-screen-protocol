# StreamScreen Go Runtime

Cross-platform screen streaming with two Go binaries:

- `server`: terminal-only capture/encode supervisor
- `client`: Ebiten viewer that decodes frames through FFmpeg and renders a live stats overlay

## Requirements

- Go 1.26+
- FFmpeg in `PATH` for the client and Windows server capture
- Linux server: `gst-launch-1.0`, `gst-inspect-1.0`, PipeWire/desktop portal, and GStreamer plugins for `pipewiresrc`, `x264enc`, `mpegtsmux`, and `udpsink`
- Windows: FFmpeg with `ddagrab` support recommended

## Commands

```bash
go build ./...
go run ./cmd/server
go run ./cmd/client
go test ./...
```

## Config

Edit `server.config.json` and `client.config.json` before running.

For one-way UDP streaming the server needs a destination host. `client_host` is included in `server.config.json` so the server can push directly to the viewer machine.

On Linux, `backend: "auto"` resolves to `portal-pipewire`, which now means:
- `org.freedesktop.portal.ScreenCast` over D-Bus via `github.com/godbus/dbus/v5`
- a GStreamer pipeline fed from the returned PipeWire remote
