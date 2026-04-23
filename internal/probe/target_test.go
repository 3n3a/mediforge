package probe

import "testing"

func TestEvaluateTarget(t *testing.T) {
	cases := []struct {
		name    string
		in      Result
		want    bool
		reason  string
	}{
		{
			name: "mp4 h264 high aac → target",
			in:   Result{Container: "mov,mp4,m4a", VideoCodec: "h264", VideoProfile: "high", AudioCodec: "aac"},
			want: true,
		},
		{
			name:   "mkv container → not target",
			in:     Result{Container: "matroska,webm", VideoCodec: "h264", VideoProfile: "high", AudioCodec: "aac"},
			want:   false,
			reason: "container=matroska,webm",
		},
		{
			name:   "hevc video → not target",
			in:     Result{Container: "mov,mp4,m4a", VideoCodec: "hevc", VideoProfile: "main", AudioCodec: "aac"},
			want:   false,
			reason: "video_codec=hevc",
		},
		{
			name:   "h264 main profile → not target",
			in:     Result{Container: "mov,mp4,m4a", VideoCodec: "h264", VideoProfile: "main", AudioCodec: "aac"},
			want:   false,
			reason: "video_profile=main",
		},
		{
			name:   "ac3 audio → not target",
			in:     Result{Container: "mov,mp4,m4a", VideoCodec: "h264", VideoProfile: "high", AudioCodec: "ac3"},
			want:   false,
			reason: "audio_codec=ac3",
		},
		{
			name:   "missing video codec surfaces 'unknown'",
			in:     Result{Container: "mov,mp4,m4a", VideoCodec: "", VideoProfile: "high", AudioCodec: "aac"},
			want:   false,
			reason: "video_codec=unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := evaluateTarget(tc.in)
			if got != tc.want {
				t.Fatalf("isTarget = %v, want %v", got, tc.want)
			}
			if !tc.want && reason != tc.reason {
				t.Fatalf("reason = %q, want %q", reason, tc.reason)
			}
		})
	}
}

func TestHasMediaExtension(t *testing.T) {
	exts := []string{"mkv", "mp4", "avi"}
	cases := []struct {
		path string
		want bool
	}{
		{"foo.mkv", true},
		{"foo.MKV", true},
		{"a/b/c.mp4", true},
		{"NoExt", false},
		{"file.txt", false},
		{"trailing.dot.", false},
		{".hidden.mkv", true},
	}
	for _, tc := range cases {
		if got := HasMediaExtension(tc.path, exts); got != tc.want {
			t.Errorf("HasMediaExtension(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
