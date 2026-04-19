# mediforge

A minimal master/worker media encoding pipeline. An Unraid server (master) watches for new media files and dispatches encoding jobs to a Mac Mini M2 (worker) over SSH. Encoded files are returned as H.264 High + AAC MP4s ready for direct play on any device via Jellyfin.

## Architecture

```
[Unraid Docker Container (Master)]
  ├── watches /inbox for new media files (inotifywait)
  ├── runs ffprobe to skip already-encoded files
  ├── scp file → Mac Mini worker inbox
  ├── ssh triggers encode.sh on worker
  ├── scp result back → Jellyfin library
  └── cleans up temp files on both sides

[Mac Mini M2 (Worker)]
  ├── ~/media-pipeline/inbox/
  ├── ~/media-pipeline/outbox/
  ├── ~/media-pipeline/encode.sh
  └── ~/media-pipeline/profile.sh  (optional, sourced by encode.sh)
```

## Prerequisites

**Master (Unraid):** Docker, docker-compose, SSH key pair for the worker.

**Worker (Mac Mini):** macOS, Homebrew, ffmpeg (`brew install ffmpeg`), Remote Login enabled (System Settings → General → Sharing → Remote Login).

---

## Worker Setup (Mac Mini)

### 1. Install ffmpeg and create directories

```bash
brew install ffmpeg
mkdir -p ~/media-pipeline/inbox ~/media-pipeline/outbox
```

### 2. Deploy scripts

```bash
scp worker/encode.sh <worker-user>@<worker-ip>:~/media-pipeline/encode.sh
scp worker/profile.sh <worker-user>@<worker-ip>:~/media-pipeline/profile.sh
ssh <worker-user>@<worker-ip> chmod +x ~/media-pipeline/encode.sh
```

### 3. Authorise the master's SSH key

```bash
# On the worker
cat >> ~/.ssh/authorized_keys << 'EOF'
<paste master's public key here>
EOF
```

### 4. Worker profile (PATH configuration)

`encode.sh` sources `~/media-pipeline/profile.sh` before running ffmpeg. The default profile already sets:

```bash
export PATH="/usr/local/bin:/opt/homebrew/bin:$PATH"
```

Edit `~/media-pipeline/profile.sh` on the worker to add any extra env vars or PATH entries your setup needs. The profile location can be overridden with the `PIPELINE_PROFILE` env var.

---

## Master Setup (Unraid)

### 1. Generate an SSH key pair (if needed)

```bash
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""
```

### 2. Configure environment

```bash
cp .env.example .env
# Edit .env — set WORKER_HOST, WORKER_USER, MASTER_INBOX, JELLYFIN_LIBRARY
```

### 3. Start the container

```bash
docker compose up -d
```

---

## Usage

### Auto-watch mode (default)

The container watches the inbox directory. Drop a media file in and it is automatically encoded and placed in the Jellyfin library.

```bash
# Start
docker compose up -d

# Logs
docker compose logs -f mediforge
```

### Manual dispatch

```bash
# Inside the container
docker compose exec mediforge /app/dispatch.sh /inbox/movie.mkv

# Outside Docker (env vars must be set)
export WORKER_HOST=192.168.1.100
export WORKER_USER=mediforge
export JELLYFIN_LIBRARY=/mnt/user/media/movies
./master/dispatch.sh /path/to/movie.mkv
```

---

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `WORKER_HOST` | Yes | — | Worker IP or hostname |
| `WORKER_USER` | Yes | — | SSH user on the worker |
| `WORKER_INBOX` | No | `~/media-pipeline/inbox` | Worker inbox path |
| `WORKER_OUTBOX` | No | `~/media-pipeline/outbox` | Worker outbox path |
| `WORKER_ENCODE_SCRIPT` | No | `~/media-pipeline/encode.sh` | Path to encode script on worker |
| `JELLYFIN_LIBRARY` | Yes | — | Destination path for encoded files |
| `SSH_KEY` | No | `/root/.ssh/id_ed25519` | SSH private key (inside container) |
| `SSH_KEY_PATH` | No | `~/.ssh/id_ed25519` | SSH private key (host path, for docker-compose) |
| `MASTER_INBOX` | No | `/inbox` | Inbox directory to watch |
| `ARCHIVE_ENABLED` | No | — | Set to any value to archive originals instead of deleting |
| `ARCHIVE_DIR` | No | `./archive` | Host path for archived originals |
| `SETTLE_DELAY` | No | `5` | Seconds to wait for a file to finish copying before dispatch |
| `PIPELINE_PROFILE` | No | `~/media-pipeline/profile.sh` | Worker profile sourced by encode.sh for PATH/env setup |

---

## Troubleshooting

**"WORKER_HOST not set"**
Copy `.env.example` to `.env` and fill in the required values.

**SSH connection refused**
- Verify Remote Login is enabled on the Mac Mini.
- Verify the key is mounted: `docker compose exec mediforge ls -la /root/.ssh/`
- Test: `docker compose exec mediforge ssh -i /root/.ssh/id_ed25519 user@host echo ok`

**ffmpeg not found on worker**
- Check `~/media-pipeline/profile.sh` includes the Homebrew bin path.
- Verify ffmpeg is installed: `which ffmpeg` on the worker.

**File not being picked up**
- Supported extensions: `.mkv .avi .mp4 .mov .ts .wmv .flv .m4v .webm`
- Check logs: `docker compose logs -f mediforge`
- Check inbox mount: `docker compose exec mediforge ls /inbox/`

**Encoding fails on worker**
- Test ffmpeg directly on the worker: `ffmpeg -i input.mkv -c:v libx264 -preset medium -crf 20 output.mp4`
- Check disk space: `df -h ~/media-pipeline/`
- Check script permissions: `ls -la ~/media-pipeline/encode.sh`

**File skipped (already in target format)**
Expected behaviour. `probe.sh` detects files already encoded as H.264 High + AAC in an MP4 container and skips them.
