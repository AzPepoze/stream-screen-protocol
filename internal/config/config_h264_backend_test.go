package config

import "testing"

func TestValidateH264BackendValue(t *testing.T) {
	cases := []struct {
		name    string
		cfg     map[string]interface{}
		key     string
		wantErr bool
	}{
		{name: "nil cfg", cfg: nil, key: "h264_encoder_backend", wantErr: false},
		{name: "missing key", cfg: map[string]interface{}{}, key: "h264_encoder_backend", wantErr: false},
		{name: "auto", cfg: map[string]interface{}{"h264_encoder_backend": "auto"}, key: "h264_encoder_backend", wantErr: false},
		{name: "gstreamer", cfg: map[string]interface{}{"h264_encoder_backend": "gstreamer"}, key: "h264_encoder_backend", wantErr: false},
		{name: "ffmpeg", cfg: map[string]interface{}{"h264_encoder_backend": "ffmpeg"}, key: "h264_encoder_backend", wantErr: false},
		{name: "invalid", cfg: map[string]interface{}{"h264_encoder_backend": "abc"}, key: "h264_encoder_backend", wantErr: true},
		{name: "wrong type", cfg: map[string]interface{}{"h264_encoder_backend": 1}, key: "h264_encoder_backend", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateH264BackendValue(tc.cfg, tc.key)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
