package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Result struct {
	Container    string
	VideoCodec   string
	VideoProfile string // lowercased
	VideoLevel   int    // 0 if unknown
	AudioCodec   string
	IsTarget     bool
	Reason       string // populated when !IsTarget
}

type ffprobeOutput struct {
	Format struct {
		FormatName string `json:"format_name"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Profile   string `json:"profile"`
		Level     int    `json:"level"`
	} `json:"streams"`
}

// Run invokes ffprobe on the given file and returns a populated Result.
func Run(ctx context.Context, ffprobeBin, path string) (Result, error) {
	cmd := exec.CommandContext(ctx, ffprobeBin,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return Result{}, fmt.Errorf("ffprobe exit %d: %s", ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
		}
		return Result{}, fmt.Errorf("ffprobe: %w", err)
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return Result{}, fmt.Errorf("ffprobe output parse: %w", err)
	}

	res := Result{
		Container: parsed.Format.FormatName,
	}
	for _, s := range parsed.Streams {
		switch s.CodecType {
		case "video":
			if res.VideoCodec == "" {
				res.VideoCodec = s.CodecName
				res.VideoProfile = strings.ToLower(s.Profile)
				res.VideoLevel = s.Level
			}
		case "audio":
			if res.AudioCodec == "" {
				res.AudioCodec = s.CodecName
			}
		}
	}
	res.IsTarget, res.Reason = evaluateTarget(res)
	return res, nil
}
