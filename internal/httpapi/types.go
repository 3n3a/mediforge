package httpapi

const (
	HeaderFilename = "X-Mediforge-Filename"
	ContentTypeBin = "application/octet-stream"
	ContentTypeMP4 = "video/mp4"
)

type ErrorResponse struct {
	Error      string `json:"error"`
	Code       string `json:"code"`
	FFmpegExit int    `json:"ffmpeg_exit,omitempty"`
	StderrTail string `json:"stderr_tail,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type HealthResponse struct {
	OK      bool   `json:"ok"`
	Busy    bool   `json:"busy"`
	Version string `json:"version"`
}
