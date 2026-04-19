#!/usr/bin/env bash
# shellcheck disable=SC2029
set -euo pipefail

# dispatch.sh — Dispatch a media file to the worker for encoding
# Usage: dispatch.sh <file>

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Config from environment
WORKER_HOST="${WORKER_HOST:?WORKER_HOST not set}"
WORKER_USER="${WORKER_USER:?WORKER_USER not set}"
WORKER_INBOX="${WORKER_INBOX:-/Users/$WORKER_USER/media-pipeline/inbox}"
WORKER_OUTBOX="${WORKER_OUTBOX:-/Users/$WORKER_USER/media-pipeline/outbox}"
WORKER_ENCODE_SCRIPT="${WORKER_ENCODE_SCRIPT:-/Users/$WORKER_USER/media-pipeline/encode.sh}"
JELLYFIN_LIBRARY="${JELLYFIN_LIBRARY:?JELLYFIN_LIBRARY not set}"
ARCHIVE_DIR="${ARCHIVE_DIR:-}"
SSH_KEY="${SSH_KEY:-/root/.ssh/id_ed25519}"
PIPELINE_PROFILE="${PIPELINE_PROFILE:-}"

SSH_OPTS=(-i "$SSH_KEY" -o StrictHostKeyChecking=no -o ConnectTimeout=10)
SSH_TARGET="${WORKER_USER}@${WORKER_HOST}"

log() { echo "[dispatch] $(date '+%Y-%m-%d %H:%M:%S') $*"; }

[[ -n "${DEBUG:-}" ]] && set -x

cleanup_worker() {
    local remote_file="$1"
    if [[ -n "${DEBUG:-}" ]]; then
        log "DEBUG: skipping cleanup of worker file: $remote_file"
        return
    fi
    log "Cleaning up worker: $remote_file"
    ssh "${SSH_OPTS[@]}" "$SSH_TARGET" "rm -f '$remote_file'" 2>/dev/null || true
}

if [[ $# -lt 1 ]]; then
    echo "Usage: dispatch.sh <file>" >&2
    exit 1
fi

INPUT="$1"

if [[ ! -f "$INPUT" ]]; then
    log "ERROR: file not found: $INPUT"
    exit 1
fi

BASENAME="$(basename "$INPUT")"
OUTPUT_NAME="${BASENAME%.*}.mp4"

# Step 1: Probe — skip if already in target format
log "Probing: $INPUT"
if "$SCRIPT_DIR/probe.sh" "$INPUT"; then
    log "SKIP: $INPUT is already in target format"
    exit 0
fi

# Step 2: scp file to worker inbox
REMOTE_INPUT="$WORKER_INBOX/$BASENAME"
log "Uploading: $INPUT -> $SSH_TARGET:$REMOTE_INPUT"
ssh "${SSH_OPTS[@]}" "$SSH_TARGET" "mkdir -p '$WORKER_INBOX' '$WORKER_OUTBOX'"
scp "${SSH_OPTS[@]}" "$INPUT" "$SSH_TARGET:$REMOTE_INPUT"

# Step 3: ssh worker to run encode
REMOTE_OUTPUT="$WORKER_OUTBOX/$OUTPUT_NAME"
WORKER_ENV="OUTBOX='$WORKER_OUTBOX' "
[[ -n "$PIPELINE_PROFILE" ]] && WORKER_ENV+="PIPELINE_PROFILE='$PIPELINE_PROFILE' "
[[ -n "${DEBUG:-}" ]] && WORKER_ENV+="DEBUG='1' "
log "Encoding on worker: $REMOTE_INPUT"
if ! ssh "${SSH_OPTS[@]}" "$SSH_TARGET" "${WORKER_ENV}bash '$WORKER_ENCODE_SCRIPT' '$REMOTE_INPUT'"; then
    log "ERROR: encoding failed on worker"
    cleanup_worker "$REMOTE_INPUT"
    exit 1
fi

# Step 4: scp encoded file back to Jellyfin library
LOCAL_OUTPUT="$JELLYFIN_LIBRARY/$OUTPUT_NAME"
log "Downloading: $SSH_TARGET:$REMOTE_OUTPUT -> $LOCAL_OUTPUT"
mkdir -p "$JELLYFIN_LIBRARY"
if ! scp "${SSH_OPTS[@]}" "$SSH_TARGET:$REMOTE_OUTPUT" "$LOCAL_OUTPUT"; then
    log "ERROR: failed to retrieve encoded file"
    cleanup_worker "$REMOTE_INPUT"
    cleanup_worker "$REMOTE_OUTPUT"
    exit 1
fi

# Step 5: Cleanup
log "Cleaning up"
cleanup_worker "$REMOTE_INPUT"
cleanup_worker "$REMOTE_OUTPUT"

if [[ -n "${DEBUG:-}" ]]; then
    log "DEBUG: skipping cleanup of master input: $INPUT"
elif [[ -n "$ARCHIVE_DIR" ]]; then
    mkdir -p "$ARCHIVE_DIR"
    mv "$INPUT" "$ARCHIVE_DIR/$BASENAME"
    log "Archived original: $ARCHIVE_DIR/$BASENAME"
else
    rm -f "$INPUT"
fi

log "DONE: $LOCAL_OUTPUT"
