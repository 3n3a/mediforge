# mediforge — Project Spec

## Overview

A minimal master/worker media encoder. The master (Go CLI in an Alpine Docker
container on Unraid) walks named media library folders in place and dispatches
encoding jobs to a Mac Mini M2 worker (Go HTTP server, native launchd) over
HTTP. Encoded files are H.264 High Level 4.1 + AAC stereo MP4s.

## Architecture

```
[Unraid Docker Container (Master — Go)]
  mediforge dispatch [library...]
    ├── walks MEDIA_LIBRARIES folders recursively in place
    ├── SQLite probe cache (WAL) skips already-checked files by (path,size,mtime)
    ├── HTTPS POST /encode → worker (streamed upload, streamed download)
    ├── extracts text subtitles from source to <stem>.default.srt sidecars (ffmpeg)
    ├── safe-replace: .<basename>.original sidecar, atomic rename of mp4 + srts
    ├── SQLite jobs ledger: attempts + retry budget + permanent failure gate
    └── optional Jellyfin /Library/Refresh trigger on success

[Mac Mini M2 (Worker — Go native via launchd)]
  mediforge-worker
    ├── POST /encode (bearer auth, one at a time via sync.Mutex)
    ├── ffmpeg: VideoToolbox GPU preferred, libx264 fallback (video + audio only)
    └── GET /health
```

Transport: HTTP + shared-secret bearer token over the trusted LAN.
No SSH, no inotify, no inbox/outbox.

## Repo layout

```
cmd/
  mediforge/              master CLI
  mediforge-worker/       worker HTTP server
internal/
  config/                 env parsing for both binaries
  logx/                   slog setup
  probe/                  ffprobe wrapper + IsTarget predicate
  cache/                  SQLite: probes + jobs + job_attempts
  scan/                   recursive walk, ext filter, skips hidden files
  dispatch/               orchestrator + flock
  client/                 HTTP client with bearer auth + 503/network retries
  httpapi/                shared DTOs + bearer auth middleware
  encode/                 worker-side ffmpeg invocation (video + audio only)
  subtitles/              master-side SRT extraction (Jellyfin sidecars)
  archive/                safe-replace (.original sidecar) + safe-archive (move to ARCHIVE_DIR)
  jellyfin/               refresh trigger (gated by env)
deploy/
  launchd/                sample plist for the worker
  fix-embedded-subs.sh    one-off backfill: mov_text -> external .srt
Dockerfile                multi-stage alpine; CGO_ENABLED=0
docker-compose.yml
.env.example
```

## Target output format

```bash
ffmpeg -y -i input.ext \
  -map 0:v -map 0:a -sn \
  -c:v libx264 -preset medium -crf 20 -pix_fmt yuv420p \
  -profile:v high -level 4.1 \
  -c:a aac -b:a 192k -ac 2 \
  -movflags +faststart \
  output.mp4
```

Video + audio only. GPU path: `-c:v h264_videotoolbox -b:v 4000k` when the
ffmpeg build supports it. The predicate for "already in target format":
container ∈ {mov, mp4, m4a}; video = h264; video profile = high; audio = aac.

## Subtitles

Text subtitle tracks are extracted on the master (not muxed into the MP4)
into external SRT sidecars beside the output file, following the Jellyfin
naming convention:
- first text track  -> `<stem>.default.srt`
- additional tracks -> `<stem>.<lang>.<N>.srt`

Bitmap subtitle codecs (`hdmv_pgs_subtitle`, `dvd_subtitle`, `dvb_subtitle`,
`xsub`) can't be losslessly converted to SRT and are logged + skipped. The
encoded MP4 + its SRT sidecars are placed atomically in a single SafeReplace
step; any failure unrolls all moves.

Files already processed by the old pipeline (embedded `mov_text`) still
satisfy the codec predicate and are NOT re-dispatched. Use
`deploy/fix-embedded-subs.sh <path>` to migrate them: extract the embedded
subs to `.srt` sidecars and stream-copy remux the MP4 without subtitles.

## Target devices

Android/iOS phones, PCs (browser/desktop), Smart TVs, Apple TV 4th gen (HEVC),
Apple TV 3rd gen (H.264 only, 1080p max) — the bottleneck device.

## Tech stack

- **Language: Go** (1.22+). stdlib `net/http`, `log/slog`, `flag`, `database/sql`.
- **SQLite: `modernc.org/sqlite`** — pure Go, no CGO, `CGO_ENABLED=0` builds.
- **Master container: alpine:3.20 + ffmpeg** — ffprobe + sub extraction on master, transcode on worker.
- **Worker: macOS native** via launchd. `brew install ffmpeg`.

Total third-party dependency: 1 (the SQLite driver; its transitive deps
notwithstanding).

## Conventions

- KISS. No web UI, no job queue (the jobs table is a ledger, not a queue), no
  message broker. Single master, single worker.
- One encoding profile. Add profiles only as concrete needs arise.
- Structured logging via `log/slog`. One line per file with `action=` key.
- Idempotent: probe cache makes repeat dispatch runs near-free; failed files
  quiesce after `MAX_RETRIES` attempts and require explicit action to retry.

## Commands

```
mediforge dispatch [library...]         Encode files not already in target format
mediforge probe <file>                  Probe a single file; print JSON
mediforge cache stats                   Row counts, jobs breakdown, DB size
mediforge cache evict <path>            Forget a file (probe + job rows)
mediforge jobs list [--status=STATUS]   Listing with optional status filter
mediforge jobs retry <path>             Reset attempts to 0 and flip status to 'failed'
mediforge version
```

## Implementation status

### Phase 1 — Bare minimum ✓
- [x] Worker HTTP server + ffmpeg pipeline (`cmd/mediforge-worker/`)
- [x] Master CLI + probe + dispatch (`cmd/mediforge/`)

### Phase 2 — Caching + safety ✓
- [x] SQLite probe cache (WAL) keyed by (path, size, mtime)
- [x] Jobs ledger with attempts + retry budget + `failed_permanent` gate
- [x] Safe-replace via `.original` sidecar (no data-loss on same-filename)
- [x] Lockfile to prevent concurrent dispatch runs

### Phase 3 — Optional integrations ✓
- [x] Optional Jellyfin `/Library/Refresh` trigger, gated by
      `JELLYFIN_INTEGRATION_ENABLED`

### Phase 4 — External subtitles ✓
- [x] Drop `mov_text` muxing from the worker encode profile (`-sn`)
- [x] Master-side extraction to `<stem>.default.srt` / `<stem>.<lang>.<N>.srt`
- [x] Bitmap subs (PGS/DVD/DVB/xsub) skipped with warning
- [x] `SafeReplaceWithSidecars` — atomic multi-file placement with rollback
- [x] `deploy/fix-embedded-subs.sh` — one-off backfill for already-processed files

### Phase 5 — Archive mode ✓
- [x] `ARCHIVE_MODE=archive` moves originals to `ARCHIVE_DIR/<lib>/<relpath>`
      instead of deleting them; cross-filesystem safe (copy+fsync+unlink fallback)
- [x] Collision handling: identical file (size+mtime) already archived → skip
      move, delete src; different file → auto-suffix (`foo.1.mkv`, `foo.2.mkv`)

### Phase 6 — Future (only if needed)
- [ ] Multiple worker support (requires worker selection strategy)
- [ ] Disk-space preflight on worker (507 Insufficient Storage)
- [ ] Alternate encoding profiles (HEVC for non-Apple-TV-3 content)
