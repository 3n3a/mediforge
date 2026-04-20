package probe

import "strings"

// evaluateTarget returns (isTarget, reason). Reason is empty when isTarget is true.
// Rules mirror master/probe.sh: container ∈ {mov, mp4, m4a} (substring); video=h264;
// video profile=high; audio=aac.
func evaluateTarget(r Result) (bool, string) {
	if !containerOK(r.Container) {
		return false, "container=" + r.Container
	}
	if r.VideoCodec != "h264" {
		return false, "video_codec=" + emptyStr(r.VideoCodec)
	}
	if r.VideoProfile != "high" {
		return false, "video_profile=" + emptyStr(r.VideoProfile)
	}
	if r.AudioCodec != "aac" {
		return false, "audio_codec=" + emptyStr(r.AudioCodec)
	}
	return true, ""
}

func containerOK(f string) bool {
	f = strings.ToLower(f)
	return strings.Contains(f, "mov") || strings.Contains(f, "mp4") || strings.Contains(f, "m4a")
}

func emptyStr(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// HasMediaExtension reports whether the given path ends with one of the
// allowed extensions (supplied without the leading dot, lowercase).
func HasMediaExtension(path string, exts []string) bool {
	dot := strings.LastIndexByte(path, '.')
	if dot < 0 || dot == len(path)-1 {
		return false
	}
	ext := strings.ToLower(path[dot+1:])
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}
