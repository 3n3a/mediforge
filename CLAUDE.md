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
    ├── safe-replace: .<basename>.original sidecar, atomic rename, fsync dir
    ├── SQLite jobs ledger: attempts + retry budget + permanent failure gate
    └── optional Jellyfin /Library/Refresh trigger on success

[Mac Mini M2 (Worker — Go native via launchd)]
  mediforge-worker
    ├── POST /encode (bearer auth, one at a time via sync.Mutex)
    ├── ffmpeg: VideoToolbox GPU preferred, libx264 fallback
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
  encode/                 worker-side ffmpeg invocation
  archive/                safe-replace via .original sidecar
  jellyfin/               refresh trigger (gated by env)
deploy/
  launchd/                sample plist for the worker
Dockerfile                multi-stage alpine; CGO_ENABLED=0
docker-compose.yml
.env.example
```

## Target output format

```bash
ffmpeg -y -i input.ext \
  -map 0:v -map 0:a -map "0:s?" \
  -c:v libx264 -preset medium -crf 20 -pix_fmt yuv420p \
  -profile:v high -level 4.1 \
  -c:a aac -b:a 192k -ac 2 \
  -c:s mov_text \
  -movflags +faststart \
  output.mp4
```

GPU path: `-c:v h264_videotoolbox -b:v 4000k` when the ffmpeg build supports it.
The predicate for "already in target format": container ∈ {mov, mp4, m4a};
video = h264; video profile = high; audio = aac.

## Target devices

Android/iOS phones, PCs (browser/desktop), Smart TVs, Apple TV 4th gen (HEVC),
Apple TV 3rd gen (H.264 only, 1080p max) — the bottleneck device.

## Tech stack

- **Language: Go** (1.22+). stdlib `net/http`, `log/slog`, `flag`, `database/sql`.
- **SQLite: `modernc.org/sqlite`** — pure Go, no CGO, `CGO_ENABLED=0` builds.
- **Master container: alpine:3.20 + ffmpeg** — ffprobe on master, encode on worker.
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

### Phase 4 — Future (only if needed)
- [ ] `archive` mode (move originals to `ARCHIVE_DIR` instead of `replace`)
- [ ] Multiple worker support (requires worker selection strategy)
- [ ] Disk-space preflight on worker (507 Insufficient Storage)
- [ ] Alternate encoding profiles (HEVC for non-Apple-TV-3 content)
