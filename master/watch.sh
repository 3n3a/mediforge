#!/usr/bin/env bash
set -euo pipefail

# watch.sh — Watch inbox directory (recursively) and dispatch new media files for encoding
# Usage: watch.sh [inbox-dir]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

INBOX="${1:-${MASTER_INBOX:-/inbox}}"
SETTLE_DELAY="${SETTLE_DELAY:-5}"      # seconds to wait for file to finish copying
SCAN_INTERVAL="${SCAN_INTERVAL:-300}"  # seconds between periodic inbox rescans

MEDIA_EXTENSIONS="mkv|avi|mp4|mov|ts|wmv|flv|m4v|webm"

log() { echo "[watch] $(date '+%Y-%m-%d %H:%M:%S') $*"; }

[[ -n "${DEBUG:-}" ]] && set -x

is_media_file() {
    local ext="${1##*.}"
    ext="${ext,,}"
    [[ "$ext" =~ ^($MEDIA_EXTENSIONS)$ ]]
}

process_file() {
    local file="$1"

    if [[ ! -f "$file" ]]; then
        log "File disappeared before processing: $file"
        return
    fi

    if ! is_media_file "$file"; then
        log "Ignoring non-media file: $file"
        return
    fi

    # Settling delay — wait for file to finish being written
    log "Waiting ${SETTLE_DELAY}s for file to settle: $file"
    sleep "$SETTLE_DELAY"

    # Verify file size is stable (not still being copied)
    local size1 size2
    size1="$(stat -c %s "$file" 2>/dev/null || stat -f %z "$file" 2>/dev/null)"
    sleep 2
    size2="$(stat -c %s "$file" 2>/dev/null || stat -f %z "$file" 2>/dev/null)"

    if [[ "$size1" != "$size2" ]]; then
        log "File still being written (size changed), retrying in ${SETTLE_DELAY}s: $file"
        sleep "$SETTLE_DELAY"
    fi

    log "Dispatching: $file"
    if "$SCRIPT_DIR/dispatch.sh" "$file" "$INBOX"; then
        log "Completed: $file"
    else
        log "FAILED: $file"
    fi
}

periodic_scan() {
    while true; do
        sleep "$SCAN_INTERVAL"
        log "Periodic scan: $INBOX"
        "$SCRIPT_DIR/dispatch.sh" "$INBOX" || log "Periodic scan error"
    done
}

mkdir -p "$INBOX"
log "Watching inbox: $INBOX (recursive, scan every ${SCAN_INTERVAL}s)"
log "Media extensions: $MEDIA_EXTENSIONS"

# Dispatch any existing files on startup
if find "$INBOX" -type f | grep -qiE "\.(${MEDIA_EXTENSIONS})$" 2>/dev/null; then
    log "Processing existing files in $INBOX"
    "$SCRIPT_DIR/dispatch.sh" "$INBOX"
fi

# Periodic rescan in background to catch anything inotifywait misses
periodic_scan &

# Recursively watch for new files using inotifywait
exec inotifywait -r -m -e close_write -e moved_to --format '%w%f' "$INBOX" | while read -r FILE; do
    process_file "$FILE"
done
