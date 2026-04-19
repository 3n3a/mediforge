# mediforge

A minimal master/worker media encoding pipeline. An Unraid server (master) watches for new media files and dispatches encoding jobs to a Mac Mini M2 (worker) over SSH. Encoded files are returned as H.264 High + AAC MP4s ready for direct play on any device via Jellyfin.

## Prerequisites

**Master (Unraid server):**
- Docker and docker-compose
- SSH key pair for connecting to the worker

**Worker (Mac Mini M2):**
- macOS with Homebrew
- ffmpeg (`brew install ffmpeg`)
- SSH server enabled (System Settings > General > Sharing > Remote Login)

## Worker Setup (Mac Mini)

1. Create a dedicated user (or use an existing one):

```bash
# Install ffmpeg
brew install ffmpeg

# Create working directories
mkdir -p ~/media-pipeline/inbox ~/media-pipeline/outbox
```

2. Copy the encode script to the worker:

```bash
scp worker/encode.sh <worker-user>@<worker-ip>:~/media-pipeline/encode.sh
chmod +x ~/media-pipeline/encode.sh
```

3. Add the master's SSH public key to the worker:

```bash
# On the worker
cat >> ~/.ssh/authorized_keys << 'EOF'
<paste master's public key here>
EOF
```

## Master Setup (Unraid)

1. Generate an SSH key pair (if you don't have one):

```bash
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""
```

2. Copy the example env file and configure it:

```bash
cp .env.example .env
# Edit .env with your worker IP, user, and paths
```

3. Build and start the container:

```bash
docker compose up -d
```

## Usage

### Auto-watch mode (default)

The container watches the inbox directory. Drop a media file in and it will be automatically encoded and placed in the Jellyfin library.

```bash
# Start watching
docker compose up -d

# Check logs
docker compose logs -f mediforge
```

### Manual dispatch

Run the dispatch script directly on a single file:

```bash
docker compose exec mediforge /app/dispatch.sh /inbox/movie.mkv
```

Or outside Docker (with env vars set):

```bash
export WORKER_HOST=192.168.1.100
export WORKER_USER=mediforge
export JELLYFIN_LIBRARY=/mnt/user/media/movies
./master/dispatch.sh /path/to/movie.mkv
```

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `WORKER_HOST` | Yes | — | Worker IP or hostname |
| `WORKER_USER` | Yes | — | SSH user on the worker |
| `WORKER_INBOX` | No | `~/media-pipeline/inbox` | Worker inbox path |
| `WORKER_OUTBOX` | No | `~/media-pipeline/outbox` | Worker outbox path |
| `WORKER_ENCODE_SCRIPT` | No | `~/media-pipeline/encode.sh` | Path to encode script on worker |
| `JELLYFIN_LIBRARY` | Yes | — | Jellyfin media library path |
| `SSH_KEY` | No | `/root/.ssh/id_ed25519` | SSH private key path (inside container) |
| `SSH_KEY_PATH` | No | `~/.ssh/id_ed25519` | SSH private key path (on host, for docker-compose) |
| `MASTER_INBOX` | No | `/inbox` | Inbox directory to watch (inside container) |
| `ARCHIVE_ENABLED` | No | — | Set to any value to archive originals instead of deleting them |
| `ARCHIVE_DIR` | No | `./archive` | Host path to store original master files |
| `SETTLE_DELAY` | No | `5` | Seconds to wait for file to finish copying |

## Troubleshooting

**"WORKER_HOST not set"**
Copy `.env.example` to `.env` and fill in the required values.

**SSH connection refused**
- Verify Remote Login is enabled on the Mac Mini (System Settings > General > Sharing > Remote Login)
- Verify the SSH key is correctly mounted: `docker compose exec mediforge ls -la /root/.ssh/`
- Test connectivity: `docker compose exec mediforge ssh -i /root/.ssh/id_ed25519 user@host echo ok`

**File not being picked up**
- Check the file has a supported extension: `.mkv`, `.avi`, `.mp4`, `.mov`, `.ts`, `.wmv`, `.flv`, `.m4v`, `.webm`
- Check container logs: `docker compose logs -f mediforge`
- Verify the inbox volume mount is correct: `docker compose exec mediforge ls /inbox/`

**Encoding fails on worker**
- SSH into the worker and test ffmpeg directly: `ffmpeg -i input.mkv -c:v libx264 -preset medium -crf 20 output.mp4`
- Check disk space on the worker: `df -h ~/media-pipeline/`
- Check encode script permissions: `ls -la ~/media-pipeline/encode.sh`

**File already in target format (skipped)**
This is expected. The probe step detects files that are already H.264 High + AAC in an MP4 container and skips them.
