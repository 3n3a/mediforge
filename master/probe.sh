#!/usr/bin/env bash
set -euo pipefail

# probe.sh — Check if a media file already matches the target format
# Exit 0 = already in target format (skip encoding)
# Exit 1 = needs encoding (prints reason)
# Usage: probe.sh <file>

[[ -n "${DEBUG:-}" ]] && set -x

if [[ $# -lt 1 ]]; then
    echo "Usage: probe.sh <file>" >&2
    exit 1
fi

FILE="$1"

if [[ ! -f "$FILE" ]]; then
    echo "File not found: $FILE" >&2
    exit 1
fi

PROBE_JSON="$(ffprobe -v quiet -print_format json -show_format -show_streams "$FILE")"

# Check container format
FORMAT="$(echo "$PROBE_JSON" | grep -o '"format_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"format_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"
case "$FORMAT" in
    *mov*|*mp4*|*m4a*) ;; # MP4-compatible container
    *)
        echo "NEEDS ENCODE: container is $FORMAT, not mp4"
        exit 1
        ;;
esac

# Check video codec
VIDEO_CODEC="$(echo "$PROBE_JSON" | grep -o '"codec_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"codec_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"
if [[ "$VIDEO_CODEC" != "h264" ]]; then
    echo "NEEDS ENCODE: video codec is $VIDEO_CODEC, not h264"
    exit 1
fi

# Check H.264 profile
VIDEO_PROFILE="$(echo "$PROBE_JSON" | grep -o '"profile"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"profile"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"
if [[ "${VIDEO_PROFILE,,}" != "high" ]]; then
    echo "NEEDS ENCODE: h264 profile is $VIDEO_PROFILE, not High"
    exit 1
fi

# Check audio codec — find the audio stream's codec
AUDIO_CODEC="$(echo "$PROBE_JSON" | python3 -c "
import sys, json
data = json.load(sys.stdin)
for s in data.get('streams', []):
    if s.get('codec_type') == 'audio':
        print(s.get('codec_name', ''))
        break
" 2>/dev/null || echo "")"

if [[ "$AUDIO_CODEC" != "aac" ]]; then
    echo "NEEDS ENCODE: audio codec is ${AUDIO_CODEC:-unknown}, not aac"
    exit 1
fi

# All checks passed
exit 0
