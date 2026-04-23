// Package subtitles extracts embedded text subtitle tracks from a media file
// into external SRT sidecar files. Runs on the master — the worker only
// handles the video+audio transcode.
//
// Naming follows Jellyfin's external-subtitle convention:
//   - first text subtitle track -> "<stem>.default.srt"
//   - additional tracks          -> "<stem>.<lang>.<N>.srt"
//
// Bitmap subtitle codecs (hdmv_pgs_subtitle, dvd_subtitle, dvb_subtitle,
// xsub) cannot be losslessly transcoded to SRT — they're skipped with a
// warning. OCR-based conversion is out of scope.
package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/3n3a/mediforge/internal/archive"
)

// bitmapCodecs are subtitle codecs that can't be losslessly converted to
// SRT. They require OCR, which we don't do.
var bitmapCodecs = map[string]bool{
	"hdmv_pgs_subtitle": true,
	"dvd_subtitle":      true,
	"dvb_subtitle":      true,
	"xsub":              true,
}

type ffprobeStream struct {
	Index     int               `json:"index"`
	CodecName string            `json:"codec_name"`
	CodecType string            `json:"codec_type"`
	Tags      map[string]string `json:"tags"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
}

// Extract enumerates subtitle streams in src via ffprobe and extracts each
// text-based stream to a temp SRT file beside src. Bitmap streams are
// logged and skipped. Returns the sidecars to move into place on encode
// success; the caller owns the tmp files (CleanupTmp them on failure).
func Extract(ctx context.Context, log *slog.Logger, ffmpegBin, ffprobeBin, src string) ([]archive.Sidecar, error) {
	streams, err := probeStreams(ctx, ffprobeBin, src)
	if err != nil {
		return nil, fmt.Errorf("probe subtitle streams: %w", err)
	}

	dir := filepath.Dir(src)
	base := filepath.Base(src)
	stem := base
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		stem = base[:dot]
	}

	var sidecars []archive.Sidecar
	textIdx := 0

	for _, s := range streams {
		if s.CodecType != "subtitle" {
			continue
		}
		if bitmapCodecs[s.CodecName] {
			log.Warn("skip bitmap sub",
				slog.String("action", "skip_sub"),
				slog.Int("stream", s.Index),
				slog.String("codec", s.CodecName),
				slog.String("src", src),
			)
			continue
		}

		lang := s.Tags["language"]
		if lang == "" {
			lang = "und"
		}

		finalPath := finalSidecarPath(dir, stem, lang, textIdx)
		tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.mediforge.sub.%d.srt.tmp", base, textIdx))

		if err := extractOne(ctx, ffmpegBin, src, s.Index, tmpPath); err != nil {
			// Clean up any sidecars written so far before returning.
			CleanupTmp(sidecars)
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("extract stream %d (%s): %w", s.Index, s.CodecName, err)
		}

		sidecars = append(sidecars, archive.Sidecar{
			TmpPath:   tmpPath,
			FinalPath: finalPath,
		})
		log.Info("extracted sub",
			slog.String("action", "extract_sub"),
			slog.Int("stream", s.Index),
			slog.String("codec", s.CodecName),
			slog.String("lang", lang),
			slog.String("final", finalPath),
		)
		textIdx++
	}

	return sidecars, nil
}

// CleanupTmp best-effort removes the TmpPath of each sidecar. Use after a
// failure between extraction and safe-replace to avoid leaving orphans.
func CleanupTmp(sidecars []archive.Sidecar) {
	for _, s := range sidecars {
		_ = os.Remove(s.TmpPath)
	}
}

func probeStreams(ctx context.Context, ffprobeBin, src string) ([]ffprobeStream, error) {
	cmd := exec.CommandContext(ctx, ffprobeBin,
		"-v", "error",
		"-select_streams", "s",
		"-show_entries", "stream=index,codec_name,codec_type:stream_tags=language",
		"-of", "json",
		src,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("parse ffprobe json: %w", err)
	}
	return out.Streams, nil
}

func extractOne(ctx context.Context, ffmpegBin, src string, streamIdx int, tmpPath string) error {
	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-nostats", "-y",
		"-i", src,
		"-map", fmt.Sprintf("0:%d", streamIdx),
		"-c:s", "srt",
		"-f", "srt",
		tmpPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 2048 {
			tail = tail[len(tail)-2048:]
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, tail)
	}
	return nil
}

// finalSidecarPath returns the Jellyfin-style external subtitle path.
// First text track -> <stem>.default.srt
// Subsequent       -> <stem>.<lang>.<N>.srt
// If the target already exists (e.g. a user-supplied .srt), append a
// .mediforge suffix so we never clobber external subs.
func finalSidecarPath(dir, stem, lang string, textIdx int) string {
	var name string
	if textIdx == 0 {
		name = stem + ".default.srt"
	} else {
		name = fmt.Sprintf("%s.%s.%d.srt", stem, lang, textIdx)
	}
	full := filepath.Join(dir, name)
	if _, err := os.Stat(full); err == nil {
		// Collision with a pre-existing sidecar (user-supplied or stale).
		// Insert .mediforge before the .srt extension.
		full = strings.TrimSuffix(full, ".srt") + ".mediforge.srt"
	}
	return full
}
