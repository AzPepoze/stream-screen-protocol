package stream

import "testing"

func TestExtendedControlFeedbackRoundTrip(t *testing.T) {
	want := ExtendedControlFeedback{
		ControlFeedback: ControlFeedback{
			FrameQueuePercent: 42,
			AudioQueuePercent: 7,
			FrameDrops:        3,
			AudioDrops:        1,
			NACKSent:          9,
		},
		RTTMS:            55,
		JitterMS:         6,
		LossPermille:     27,
		DeliveryRateKbps: 18432,
	}
	got, err := UnmarshalExtendedControlFeedback(MarshalExtendedControlFeedback(want))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestExtendedControlFeedbackAcceptsV1(t *testing.T) {
	legacy := ControlFeedback{FrameQueuePercent: 20, NACKSent: 4}
	got, err := UnmarshalExtendedControlFeedback(MarshalControlFeedback(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if got.ControlFeedback != legacy {
		t.Fatalf("got %#v want %#v", got.ControlFeedback, legacy)
	}
	if got.RTTMS != 0 || got.LossPermille != 0 || got.DeliveryRateKbps != 0 {
		t.Fatalf("legacy feedback should not invent v2 metrics: %#v", got)
	}
}

func TestProbeRoundTripHeader(t *testing.T) {
	packet := MarshalProbe(123, 456)
	var h PacketHeader
	if err := h.Unmarshal(packet); err != nil {
		t.Fatal(err)
	}
	if h.PacketType != CSPPacketTypeProbe || h.FrameSeq != 123 || h.Timestamp != 456 {
		t.Fatalf("unexpected probe header: %#v", h)
	}
}
