package capture

import "context"

type disabledSource struct {
	frames chan []byte
}

func newDisabledSource() Source {
	ch := make(chan []byte)
	close(ch)
	return &disabledSource{frames: ch}
}

func (s *disabledSource) Start(_ context.Context) error { return nil }

func (s *disabledSource) Frames() <-chan []byte { return s.frames }

func (s *disabledSource) Close() error { return nil }
