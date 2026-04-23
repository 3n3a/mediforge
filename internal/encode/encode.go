package encode

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Options struct {
	FFmpegBin  string
	Encoder    string // "auto" | "videotoolbox" | "libx264"
	Input      string
	Output     string
	StderrTail int // bytes of trailing stderr to keep on error; 0 = default 8192
}

type Result struct {
	EncoderUsed string // "h264_videotoolbox" or "libx264"
	StderrTail  string
}

// Encode runs ffmpeg to transcode Input -> Output with the target profile.
// Video+audio only — subtitles are handled out-of-band on the master as
// external SRT sidecars (see internal/subtitles). -sn ensures no subtitle
// stream leaks into the output regardless of what exists in the input.
// GPU via h264_videotoolbox when available, libx264 -preset medium -crf 20
// fallback; H.264 High / Level 4.1, AAC stereo 192k, +faststart.
func Encode(ctx context.Context, opts Options) (Result, error) {
	encoder, err := pickEncoder(ctx, opts.FFmpegBin, opts.Encoder)
	if err != nil {
		return Result{}, err
	}

	args := []string{"-y", "-hide_banner", "-nostats", "-i", opts.Input,
		"-map", "0:v", "-map", "0:a", "-sn"}

	if encoder == "h264_videotoolbox" {
		args = append(args,
			"-c:v", "h264_videotoolbox", "-b:v", "4000k", "-pix_fmt", "yuv420p",
			"-profile:v", "high", "-level", "4.1",
		)
	} else {
		args = append(args,
			"-c:v", "libx264", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p",
			"-profile:v", "high", "-level", "4.1",
		)
	}

	args = append(args,
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		"-movflags", "+faststart",
		opts.Output,
	)

	cmd := exec.CommandContext(ctx, opts.FFmpegBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		tailBytes := opts.StderrTail
		if tailBytes <= 0 {
			tailBytes = 8192
		}
		tail := stderr.Bytes()
		if len(tail) > tailBytes {
			tail = tail[len(tail)-tailBytes:]
		}
		exitCode := -1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		return Result{EncoderUsed: encoder, StderrTail: string(tail)},
			fmt.Errorf("ffmpeg exit %d: %s", exitCode, strings.TrimSpace(lastLine(string(tail))))
	}

	return Result{EncoderUsed: encoder}, nil
}

func pickEncoder(ctx context.Context, bin, pref string) (string, error) {
	switch pref {
	case "libx264":
		return "libx264", nil
	case "videotoolbox":
		return "h264_videotoolbox", nil
	}
	// auto
	available, err := hasVideoToolbox(ctx, bin)
	if err != nil {
		return "", err
	}
	if available {
		return "h264_videotoolbox", nil
	}
	return "libx264", nil
}

func hasVideoToolbox(ctx context.Context, bin string) (bool, error) {
	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-encoders")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("ffmpeg -encoders: %w", err)
	}
	return bytes.Contains(out, []byte("h264_videotoolbox")), nil
}

func lastLine(s string) string {
	if i := strings.LastIndex(strings.TrimRight(s, "\n"), "\n"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s)
}
