# mediforge — Project Spec

## Overview

A minimal master/worker media encoding pipeline. An Unraid server (master) dispatches encoding jobs to a Mac Mini M2 (worker) over SSH. Encoded files are returned to the Unraid Jellyfin library as H.264 High + AAC MP4s.

## Architecture

```
[Unraid Docker Container (Master)]
  ├── watches /inbox for new media files (inotifywait via watch.sh)
  ├── probe.sh: ffprobe check — skip if already H.264 High + AAC in MP4
  ├── scp file → Mac Mini worker inbox
  ├── ssh triggers encode.sh on worker
  ├── scp result back → Jellyfin library
  └── cleans up temp files on both sides

[Mac Mini M2 (Worker)]
  ├── ~/media-pipeline/inbox/
  ├── ~/media-pipeline/outbox/
  ├── ~/media-pipeline/encode.sh   — ffmpeg encode script
  └── ~/media-pipeline/profile.sh  — sourced by encode.sh; sets PATH/env
```

Communication: SSH with key-based auth. No web framework, no message queue, no database.

## Repo Layout

```
master/
  dispatch.sh     — scp → ssh encode → scp back → cleanup
  probe.sh        — ffprobe check; exit 0 = skip, exit 1 = needs encode
  watch.sh        — inotifywait loop; calls dispatch.sh per file
worker/
  encode.sh       — ffmpeg encode; sources profile.sh for PATH
  profile.sh      — default profile; copy to ~/media-pipeline/profile.sh on worker
Dockerfile        — Alpine image with openssh-client, ffmpeg, bash, inotify-tools, python3
docker-compose.yml
.env.example
```

## Target Output Format

```bash
ffmpeg -i input.mkv \
  -c:v libx264 -preset medium -crf 20 \
  -profile:v high -level 4.1 \
  -c:a aac -b:a 192k -ac 2 \
  -movflags +faststart \
  -map 0:v:0 -map 0:a:0 \
  output.mp4
```

- **MP4 + H.264 High Profile Level 4.1** — direct-plays on every target device including Apple TV 3rd gen (bottleneck device).
- **AAC stereo 192k** — universal compatibility.
- **`-movflags +faststart`** — moov atom at front for immediate Jellyfin streaming.
- **Software encode (libx264)** — better quality-per-bit on M2's 8 performance cores.

## Target Devices

- Android/iOS phones, PCs (browser/desktop), Smart TVs
- Apple TV 4th gen (HEVC capable)
- Apple TV 3rd gen (H.264 only, 1080p max) ← bottleneck

## Tech Stack

- **Language: Bash** — ffmpeg is designed to be shell-driven. Graduate to Go only if complex state, concurrency, or health checks become necessary.
- **Master container: Alpine Linux** — openssh-client, ffmpeg (for ffprobe), bash, inotify-tools, python3
- **Worker: macOS** — Homebrew ffmpeg, no containerisation
- **Transport: scp/ssh** — key-based auth, no passwords

## Worker PATH / Environment

SSH non-login shells don't source shell profiles, so Homebrew binaries may be missing from PATH. `encode.sh` sources `~/media-pipeline/profile.sh` before invoking ffmpeg. The default `worker/profile.sh` prepends `/usr/local/bin` and `/opt/homebrew/bin`. Override the profile path with `PIPELINE_PROFILE`.

## Conventions

- KISS. No web UI, no JS frameworks, no unnecessary dependencies.
- One encoding profile. Add profiles only as concrete needs arise.
- Shell scripts are the default. Only introduce Go when bash becomes the bottleneck.
- All ffmpeg commands are logged with full arguments for reproducibility.
- Idempotent: re-running on an already-encoded file is a no-op (probe check + output-newer-than-input guard).

## Implementation Status

### Phase 1 — Bare minimum ✓
- [x] Worker encode script (`worker/encode.sh`)
- [x] Worker profile script (`worker/profile.sh`) — PATH setup for Homebrew
- [x] Master dispatch script (`master/dispatch.sh`)
- [x] ffprobe pre-check (`master/probe.sh`)

### Phase 2 — Automation ✓
- [x] Master watch loop (`master/watch.sh`) — inotifywait + settle/stability check
- [x] ffprobe pre-check to skip already-encoded files
- [x] Logging (stdout, redirectable)
- [x] Dockerfile + docker-compose

### Phase 3 — Hardening (only if needed)
- [ ] Job queue (file-based or SQLite) to survive restarts
- [ ] Multiple worker support
- [ ] Completion/failure notifications (webhook, email, Unraid notification)
- [ ] Health check endpoint (if rewritten in Go)
- [ ] Optional HEVC profile for non-Apple-TV-3 content
