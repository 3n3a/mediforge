#!/usr/bin/env bash
# fix-embedded-subs.sh
#
# One-off backfill for files already re-encoded with the old pipeline (which
# muxed subtitles into the MP4 as mov_text). Extracts every embedded text
# subtitle track to an external .srt sidecar following the Jellyfin
# convention (<stem>.default.srt for the first, <stem>.<lang>.<N>.srt for
# the rest), then stream-copy remuxes the MP4 without subtitle tracks.
#
# Bitmap subs (PGS / DVD / DVB) cannot be converted to SRT without OCR and
# are skipped with a warning.
#
# Usage:
#   deploy/fix-embedded-subs.sh <file-or-dir> [file-or-dir ...]
#
# Safe to re-run: files with no subtitle streams are skipped.
#
# by 3n3a

set -euo pipefail

BITMAP_CODECS_RE='^(hdmv_pgs_subtitle|dvd_subtitle|dvb_subtitle|xsub)$'

usage() {
  cat <<EOF
Usage: $0 <file-or-dir> [file-or-dir ...]

Extracts embedded subtitles to <stem>.default.srt (and .<lang>.N.srt for extras)
then rewrites the MP4 without subtitle tracks via stream copy.
EOF
  exit 2
}

# Enumerate subtitle streams as "<index>|<codec>|<lang>" lines. Empty output
# means no subtitle streams.
list_sub_streams() {
  local input="$1"
  ffprobe -v error -select_streams s \
    -show_entries stream=index,codec_name:stream_tags=language \
    -of csv=p=0 "$input"
}

process_file() {
  local input="$1"
  local dir base stem ext
  dir=$(dirname "$input")
  base=$(basename "$input")
  stem="${base%.*}"
  ext="${base##*.}"

  case "${ext,,}" in
    mp4|m4v|mov) ;;
    *) echo "skip (not an mp4-family file): $input"; return 0 ;;
  esac

  local streams
  streams=$(list_sub_streams "$input" || true)
  if [[ -z "$streams" ]]; then
    echo "skip (no subs): $input"
    return 0
  fi

  local total text_count=0 bitmap_count=0
  total=$(printf '%s\n' "$streams" | wc -l | tr -d ' ')
  echo "process: $input ($total subtitle stream(s))"

  # Pass 1: extract text subs; count bitmap skips.
  local text_idx=0
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    local idx codec lang
    idx=$(printf '%s' "$line" | awk -F, '{print $1}')
    codec=$(printf '%s' "$line" | awk -F, '{print $2}')
    lang=$(printf '%s' "$line" | awk -F, '{print $3}')
    [[ -z "$lang" ]] && lang="und"

    if [[ "$codec" =~ $BITMAP_CODECS_RE ]]; then
      echo "  skip bitmap sub: stream=$idx codec=$codec lang=$lang"
      bitmap_count=$((bitmap_count + 1))
      continue
    fi

    local out
    if [[ $text_idx -eq 0 ]]; then
      out="$dir/$stem.default.srt"
    else
      out="$dir/$stem.$lang.$text_idx.srt"
    fi
    if [[ -e "$out" ]]; then
      out="${out%.srt}.mediforge.srt"
    fi

    echo "  extract: stream=$idx codec=$codec lang=$lang -> $out"
    ffmpeg -hide_banner -nostats -y -loglevel error \
      -i "$input" -map "0:$idx" -c:s srt "$out"
    text_count=$((text_count + 1))
    text_idx=$((text_idx + 1))
  done <<<"$streams"

  # Pass 2: stream-copy remux without subs. Uses a temp file + atomic rename.
  local tmp="$dir/.$base.mediforge.tmp"
  echo "  remux (no subs): -> $input"
  ffmpeg -hide_banner -nostats -y -loglevel error \
    -i "$input" \
    -map 0:v -map 0:a -sn \
    -c copy \
    -movflags +faststart \
    "$tmp"

  mv -f "$tmp" "$input"
  echo "  done: extracted=$text_count bitmap_skipped=$bitmap_count"
}

walk_arg() {
  local target="$1"
  if [[ -d "$target" ]]; then
    # NUL-delimited so filenames with spaces/newlines are safe.
    while IFS= read -r -d '' file; do
      process_file "$file" || echo "error processing $file (continuing)"
    done < <(find "$target" -type f \( -iname '*.mp4' -o -iname '*.m4v' -o -iname '*.mov' \) -print0)
  elif [[ -f "$target" ]]; then
    process_file "$target" || echo "error processing $target (continuing)"
  else
    echo "skip (not a file or dir): $target"
  fi
}

main() {
  [[ $# -lt 1 ]] && usage
  for arg in "$@"; do
    walk_arg "$arg"
  done
}

main "$@"
