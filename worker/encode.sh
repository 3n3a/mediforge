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

if ffmpeg -encoders 2>/dev/null | grep -q h264_videotoolbox; then
    log "Encoder: h264_videotoolbox (GPU)"
    FFMPEG_CMD=(
        ffmpeg -y -i "$INPUT"
        -c:v h264_videotoolbox -b:v 4000k -pix_fmt yuv420p
        -profile:v high -level 4.1
        -c:a aac -b:a 192k -ac 2
        -movflags +faststart
        -map 0:v:0 -map 0:a:0
        "$OUTPUT"
    )
else
    log "Encoder: libx264 (CPU fallback)"
    FFMPEG_CMD=(
        ffmpeg -y -i "$INPUT"
        -c:v libx264 -preset medium -crf 20 -pix_fmt yuv420p
        -profile:v high -level 4.1
        -c:a aac -b:a 192k -ac 2
        -movflags +faststart
        -map 0:v:0 -map 0:a:0
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
