# Media Pipeline — Project Spec

## Overview

A minimal master/worker media encoding pipeline. An Unraid server (master) dispatches encoding jobs to a Mac Mini M2 (worker) over SSH. Encoded files are returned to the Unraid Jellyfin library.

## Architecture

```
[Unraid Docker Container (Master)]
  ├── watches inbox folder for new media files
  ├── runs ffprobe to determine encoding needs
  ├── scp file → Mac Mini worker
  ├── ssh triggers encode on worker
  ├── scp result back → Jellyfin library
  └── cleans up temp files on both sides

[Mac Mini M2 (Worker)]
  ├── ffmpeg installed via Homebrew
  ├── inbox/outbox directory pair
  └── encode script: inbox → ffmpeg → outbox
```

Communication: **SSH with key-based auth.** No web framework, no message queue, no database.

## Target Output Format

**Universal direct-play profile (MP4/H.264):**

```bash
ffmpeg -i input.mkv \
  -c:v libx264 -preset medium -crf 20 \
  -profile:v high -level 4.1 \
  -c:a aac -b:a 192k -ac 2 \
  -movflags +faststart \
  -map 0:v:0 -map 0:a:0 \
  output.mp4
```

### Why these choices

- **Container: MP4** — not WebM. WebM (VP9/AV1) doesn't direct-play on Apple TV 3/4 or many TV apps. MP4 with H.264+AAC direct-plays on every target device.
- **Codec: H.264 High Profile, Level 4.1** — required for Apple TV 3rd gen compatibility (the weakest target device). If Apple TV 3 support is dropped later, switch to HEVC for ~40% bitrate savings.
- **`-movflags +faststart`** — moves moov atom to front of file so Jellyfin can start streaming immediately.
- **Audio: AAC stereo 192k** — universal compatibility. Consider adding AC3 passthrough as a second audio track for surround on Apple TV 4 / TVs.
- **Software encode (`libx264`)** over VideoToolbox — better quality-per-bit. M2 has 8 performance cores, fast enough. Use VideoToolbox only if bulk speed matters more than quality.

### No adaptive bitrate (ABR) for now

Jellyfin doesn't natively serve pre-encoded HLS/DASH ladders from disk. Encode one good 1080p version; let Jellyfin handle on-the-fly transcoding for low-bandwidth clients. Revisit ABR only if serving many concurrent streams.

## Target Playback Devices

- Android phones
- iOS phones
- PCs (web browser / Jellyfin desktop)
- Smart TVs
- Apple TV 4th gen (HEVC capable)
- Apple TV 3rd gen (H.264 only, 1080p max) ← **bottleneck device**

## Tech Stack

- **Language: Bash** — start here. ffmpeg is designed to be driven by shell. Graduate to **Go** if/when a proper API, job queue, or health checks are needed.
- **Master container: Alpine Linux** — with `openssh-client`, `ffmpeg` (for ffprobe), `bash`
- **Worker: macOS** — Homebrew ffmpeg, no containerization needed
- **Transport: scp/ssh** — key-based auth, no passwords

## Master (Unraid Docker Container)

### Responsibilities

1. Watch an inbox directory for new media files
2. Run `ffprobe` on new files to decide if encoding is needed (skip if already H.264 High + AAC in MP4)
3. `scp` source file to worker inbox
4. `ssh` worker to run encode command
5. Poll worker outbox (or block on SSH command completion)
6. `scp` encoded file back to Jellyfin library path
7. Clean up source from inbox and temp files on worker

### Docker container requirements

- Base: `alpine:latest`
- Packages: `openssh-client`, `ffmpeg`, `bash`, `inotify-tools` (or use polling loop)
- Volume mounts: inbox folder, Jellyfin media library folder
- SSH private key mounted as secret/volume

## Worker (Mac Mini M2)

### Responsibilities

1. Accept incoming files via scp into inbox directory
2. Run ffmpeg encode when triggered by master over SSH
3. Place output in outbox directory
4. Master retrieves output; worker cleans up

### Setup

- `brew install ffmpeg`
- Dedicated user account for SSH access
- `~/.ssh/authorized_keys` with master's public key
- Inbox: `~/media-pipeline/inbox/`
- Outbox: `~/media-pipeline/outbox/`

## Implementation Plan

### Phase 1 — Bare minimum

- [ ] Worker encode script (`encode.sh`): takes input path, outputs to outbox
- [ ] Master dispatch script (`dispatch.sh`): scp → ssh → scp → cleanup
- [ ] Manual trigger (run `dispatch.sh <file>`)

### Phase 2 — Automation

- [ ] Master watch loop (inotify or cron-based polling on inbox dir)
- [ ] ffprobe pre-check to skip files already in target format
- [ ] Basic logging (stdout, redirectable to file)
- [ ] Dockerfile for master container

### Phase 3 — Hardening (only if needed)

- [ ] Job queue (file-based or SQLite) to survive restarts
- [ ] Multiple worker support
- [ ] Notification on completion/failure (webhook, email, or Unraid notification)
- [ ] Health check endpoint (if rewritten in Go)
- [ ] Optional HEVC profile for non-Apple-TV-3 content

## Conventions

- KISS. No web UI, no JS frameworks, no unnecessary dependencies.
- One encoding profile to start. Add profiles as concrete needs arise.
- Shell scripts are the default. Only introduce Go/compiled language when bash becomes the bottleneck (complex state, concurrency, error handling).
- All ffmpeg commands must be logged with full arguments for reproducibility.
- Idempotent operations: re-running on an already-encoded file should be a no-op.
