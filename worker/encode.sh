#!/usr/bin/env bash
set -euo pipefail

# encode.sh — Encode a media file to H.264 High + AAC in MP4
# Usage: encode.sh <input-file>

OUTBOX="${OUTBOX:-$HOME/media-pipeline/outbox}"
PIPELINE_PROFILE="${PIPELINE_PROFILE:-$HOME/media-pipeline/profile.sh}"

log() { echo "[encode] $(date '+%Y-%m-%d %H:%M:%S') $*"; }

[[ -n "${DEBUG:-}" ]] && set -x

[[ -f "$PIPELINE_PROFILE" ]] && source "$PIPELINE_PROFILE"

if ! command -v ffmpeg &>/dev/null; then
    log "ERROR: ffmpeg not found. Install it with: brew install ffmpeg"
    exit 1
fi

if [[ $# -lt 1 ]]; then
    echo "Usage: encode.sh <input-file>" >&2
    exit 1
fi

INPUT="$1"

if [[ ! -f "$INPUT" ]]; then
    log "ERROR: input file not found: $INPUT"
    exit 1
fi

BASENAME="$(basename "$INPUT")"
OUTPUT_NAME="${BASENAME%.*}.mp4"
OUTPUT="$OUTBOX/$OUTPUT_NAME"

mkdir -p "$OUTBOX"

# Idempotent: skip if output exists and is newer than input
if [[ -f "$OUTPUT" && "$OUTPUT" -nt "$INPUT" ]]; then
    log "SKIP: output already exists and is newer than input: $OUTPUT"
    exit 0
fi

log "Encoding: $INPUT -> $OUTPUT"

# Common mapping: all video, all audio, all subtitles (? = don't fail if absent)
# Subtitles converted to mov_text (MP4-compatible). Other audio tracks copied as-is
# if already AAC-friendly; otherwise re-encoded. We re-encode audio track 0 to AAC
# stereo for compatibility, and copy additional audio tracks.
MAP_ARGS=(
    -map 0:v -map 0:a -map "0:s?"
)

if ffmpeg -encoders 2>/dev/null | grep -q h264_videotoolbox; then
    log "Encoder: h264_videotoolbox (GPU)"
    FFMPEG_CMD=(
        ffmpeg -y -i "$INPUT"
        "${MAP_ARGS[@]}"
        -c:v h264_videotoolbox -b:v 4000k -pix_fmt yuv420p
        -profile:v high -level 4.1
        -c:a aac -b:a 192k -ac 2
        -c:s mov_text
        -movflags +faststart
        "$OUTPUT"
    )
else
    log "Encoder: libx264 (CPU fallback)"
    FFMPEG_CMD=(
        ffmpeg -y -i "$INPUT"
        "${MAP_ARGS[@]}"
        -c:v libx264 -preset medium -crf 20 -pix_fmt yuv420p
        -profile:v high -level 4.1
        -c:a aac -b:a 192k -ac 2
        -c:s mov_text
        -movflags +faststart
        "$OUTPUT"
    )
fi

log "Command: ${FFMPEG_CMD[*]}"

if "${FFMPEG_CMD[@]}"; then
    log "SUCCESS: $OUTPUT"
else
    log "FAILED: ffmpeg exited with $?"
    if [[ -n "${DEBUG:-}" ]]; then
        log "DEBUG: skipping cleanup of output: $OUTPUT"
    else
        rm -f "$OUTPUT"
    fi
    exit 1
fi
