//go:build linux

package portal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"streamscreen/internal/config"
)

const (
	desktopBusName        = "org.freedesktop.portal.Desktop"
	desktopObjectPath     = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	screenCastInterface   = "org.freedesktop.portal.ScreenCast"
	requestInterface      = "org.freedesktop.portal.Request"
	sessionInterface      = "org.freedesktop.portal.Session"
	defaultCursorModeBits = uint32(2)
	defaultSourceTypeBits = uint32(1)
)

type StreamInfo struct {
	NodeID     uint32
	Properties map[string]dbus.Variant
}

type ScreenCastSession struct {
	conn          *dbus.Conn
	sessionHandle dbus.ObjectPath
	remoteFile    *os.File
	Streams       []StreamInfo
}

func (s *ScreenCastSession) RemoteFile() *os.File {
	return s.remoteFile
}

func (s *ScreenCastSession) Close() error {
	var errs []error

	if s.conn != nil && s.sessionHandle != "" {
		obj := s.conn.Object(desktopBusName, s.sessionHandle)
		call := obj.Call(sessionInterface+".Close", 0)
		if call.Err != nil {
			errs = append(errs, call.Err)
		}
	}

	if s.remoteFile != nil {
		if err := s.remoteFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if s.conn != nil {
		if err := s.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func StartScreenCast(ctx context.Context, cfg config.ServerConfig) (*ScreenCastSession, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect session bus: %w", err)
	}

	cleanup := func(err error) (*ScreenCastSession, error) {
		_ = conn.Close()
		return nil, err
	}

	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(requestInterface),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return cleanup(fmt.Errorf("subscribe portal responses: %w", err))
	}
	defer conn.RemoveMatchSignal(
		dbus.WithMatchInterface(requestInterface),
		dbus.WithMatchMember("Response"),
	)

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	desktop := conn.Object(desktopBusName, desktopObjectPath)

	createToken := uniqueToken("create")
	createOptions := map[string]dbus.Variant{
		"handle_token":         dbus.MakeVariant(createToken),
		"session_handle_token": dbus.MakeVariant(uniqueToken("session")),
	}

	var createRequest dbus.ObjectPath
	if err := desktop.CallWithContext(ctx, screenCastInterface+".CreateSession", 0, createOptions).Store(&createRequest); err != nil {
		return cleanup(fmt.Errorf("portal CreateSession: %w", err))
	}

	createResponse, err := waitForResponse(ctx, signals, createRequest)
	if err != nil {
		return cleanup(fmt.Errorf("portal CreateSession response: %w", err))
	}

	sessionHandle, err := objectPathValue(createResponse.Results["session_handle"])
	if err != nil {
		return cleanup(fmt.Errorf("portal session handle: %w", err))
	}

	selectOptions := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(uniqueToken("select")),
		"multiple":     dbus.MakeVariant(false),
		"types":        dbus.MakeVariant(sourceTypeBits(cfg.Capture.SourceType)),
		"cursor_mode":  dbus.MakeVariant(cursorModeBits(cfg.Capture.CursorMode)),
	}
	if cfg.Audio.Enabled {
		// Request system audio stream when available in portal implementation.
		selectOptions["audio"] = dbus.MakeVariant(true)
	}

	var selectRequest dbus.ObjectPath
	if err := desktop.CallWithContext(ctx, screenCastInterface+".SelectSources", 0, sessionHandle, selectOptions).Store(&selectRequest); err != nil {
		return cleanup(fmt.Errorf("portal SelectSources: %w", err))
	}

	if _, err := waitForResponse(ctx, signals, selectRequest); err != nil {
		return cleanup(fmt.Errorf("portal SelectSources response: %w", err))
	}

	startOptions := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(uniqueToken("start")),
	}

	var startRequest dbus.ObjectPath
	if err := desktop.CallWithContext(ctx, screenCastInterface+".Start", 0, sessionHandle, "", startOptions).Store(&startRequest); err != nil {
		return cleanup(fmt.Errorf("portal Start: %w", err))
	}

	startResponse, err := waitForResponse(ctx, signals, startRequest)
	if err != nil {
		return cleanup(fmt.Errorf("portal Start response: %w", err))
	}

	streams, err := parseStreams(startResponse.Results["streams"])
	if err != nil {
		return cleanup(fmt.Errorf("portal streams: %w", err))
	}
	if len(streams) == 0 {
		return cleanup(errors.New("portal returned no streams"))
	}

	var remoteFD dbus.UnixFD
	if err := desktop.CallWithContext(ctx, screenCastInterface+".OpenPipeWireRemote", 0, sessionHandle, map[string]dbus.Variant{}).Store(&remoteFD); err != nil {
		return cleanup(fmt.Errorf("portal OpenPipeWireRemote: %w", err))
	}

	file := os.NewFile(uintptr(remoteFD), "portal-pipewire-remote")
	if file == nil {
		return cleanup(errors.New("portal returned an invalid PipeWire file descriptor"))
	}

	return &ScreenCastSession{
		conn:          conn,
		sessionHandle: sessionHandle,
		remoteFile:    file,
		Streams:       streams,
	}, nil
}

type requestResponse struct {
	Code    uint32
	Results map[string]dbus.Variant
}

func waitForResponse(ctx context.Context, signals <-chan *dbus.Signal, requestPath dbus.ObjectPath) (requestResponse, error) {
	for {
		select {
		case <-ctx.Done():
			return requestResponse{}, ctx.Err()
		case sig := <-signals:
			if sig == nil || sig.Path != requestPath {
				continue
			}
			if len(sig.Body) != 2 {
				return requestResponse{}, fmt.Errorf("unexpected portal response body for %s", requestPath)
			}

			code, ok := sig.Body[0].(uint32)
			if !ok {
				return requestResponse{}, fmt.Errorf("unexpected response code type %T", sig.Body[0])
			}

			results, ok := sig.Body[1].(map[string]dbus.Variant)
			if !ok {
				return requestResponse{}, fmt.Errorf("unexpected response payload type %T", sig.Body[1])
			}

			if code != 0 {
				return requestResponse{}, fmt.Errorf("portal request %s failed with code %d", requestPath, code)
			}

			return requestResponse{Code: code, Results: results}, nil
		}
	}
}

func objectPathValue(v dbus.Variant) (dbus.ObjectPath, error) {
	switch value := v.Value().(type) {
	case dbus.ObjectPath:
		return value, nil
	case string:
		return dbus.ObjectPath(value), nil
	default:
		return "", fmt.Errorf("unexpected object path type %T", v.Value())
	}
}

func parseStreams(v dbus.Variant) ([]StreamInfo, error) {
	value := v.Value()
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || rv.Kind() != reflect.Slice {
		return nil, fmt.Errorf("unexpected streams payload type %T", value)
	}

	streams := make([]StreamInfo, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		item := rv.Index(i).Interface()
		nodeID, props, err := parseStreamEntry(item)
		if err != nil {
			return nil, err
		}
		streams = append(streams, StreamInfo{
			NodeID:     nodeID,
			Properties: props,
		})
	}

	return streams, nil
}

func parseStreamEntry(item any) (uint32, map[string]dbus.Variant, error) {
	switch value := item.(type) {
	case []interface{}:
		return parseStreamTuple(value)
	}

	rv := reflect.ValueOf(item)
	switch rv.Kind() {
	case reflect.Struct:
		if rv.NumField() < 2 {
			return 0, nil, fmt.Errorf("unexpected stream struct shape %T", item)
		}
		nodeID, err := asUint32(rv.Field(0).Interface())
		if err != nil {
			return 0, nil, err
		}
		props, err := asVariantMap(rv.Field(1).Interface())
		if err != nil {
			return 0, nil, err
		}
		return nodeID, props, nil
	case reflect.Array, reflect.Slice:
		if rv.Len() < 2 {
			return 0, nil, fmt.Errorf("unexpected stream tuple shape %T", item)
		}
		nodeID, err := asUint32(rv.Index(0).Interface())
		if err != nil {
			return 0, nil, err
		}
		props, err := asVariantMap(rv.Index(1).Interface())
		if err != nil {
			return 0, nil, err
		}
		return nodeID, props, nil
	default:
		return 0, nil, fmt.Errorf("unsupported stream entry type %T", item)
	}
}

func parseStreamTuple(values []any) (uint32, map[string]dbus.Variant, error) {
	if len(values) < 2 {
		return 0, nil, errors.New("portal stream tuple is too short")
	}
	nodeID, err := asUint32(values[0])
	if err != nil {
		return 0, nil, err
	}
	props, err := asVariantMap(values[1])
	if err != nil {
		return 0, nil, err
	}
	return nodeID, props, nil
}

func asVariantMap(v any) (map[string]dbus.Variant, error) {
	switch value := v.(type) {
	case map[string]dbus.Variant:
		return value, nil
	case map[string]interface{}:
		out := make(map[string]dbus.Variant, len(value))
		for key, item := range value {
			if variant, ok := item.(dbus.Variant); ok {
				out[key] = variant
				continue
			}
			out[key] = dbus.MakeVariant(item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected stream properties type %T", v)
	}
}

func asUint32(v any) (uint32, error) {
	switch value := v.(type) {
	case uint32:
		return value, nil
	case uint64:
		return uint32(value), nil
	case int:
		return uint32(value), nil
	case int32:
		return uint32(value), nil
	case int64:
		return uint32(value), nil
	case string:
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return 0, err
		}
		return uint32(parsed), nil
	default:
		return 0, fmt.Errorf("unexpected uint32 type %T", v)
	}
}

func sourceTypeBits(value string) uint32 {
	switch strings.ToLower(value) {
	case "window":
		return 2
	case "virtual":
		return 4
	case "any":
		return 1 | 2 | 4
	default:
		return defaultSourceTypeBits
	}
}

func cursorModeBits(value string) uint32 {
	switch strings.ToLower(value) {
	case "hidden":
		return 1
	case "metadata":
		return 4
	default:
		return defaultCursorModeBits
	}
}

func uniqueToken(prefix string) string {
	return fmt.Sprintf("streamscreen_%s_%d", prefix, time.Now().UnixNano())
}
