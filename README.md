# mediforge

A minimal master/worker media encoder. An Unraid Docker container (**master**)
walks named Jellyfin library folders in-place and dispatches encoding jobs to a
Mac Mini M2 (**worker**) over HTTP. Encoded files are H.264 High Level 4.1 + AAC
stereo MP4s — direct-playable on every target device including Apple TV 3rd gen.

## Architecture

```
[Unraid Docker (master, Go)]                 [Mac Mini M2 (worker, Go native)]
  mediforge dispatch [library...]              mediforge-worker serves HTTP
    ├─ walks /media/tv, /media/movies           ├─ POST /encode (bearer auth)
    ├─ SQLite probe cache (WAL)                 ├─ ffmpeg (VideoToolbox/libx264)
    ├─ POST /encode → worker                    └─ GET  /health
    ├─ safe-replace via .original sidecar
    └─ tracks jobs + retries in SQLite
```

No inotify. No inbox/outbox. No periodic scans. Manual or cron-driven.

## Key behaviour

- **In-place encoding.** `mediforge dispatch tv` walks `/media/tv` recursively,
  probes every video file, and encodes any that don't already match the target
  format. The encoded file replaces the original in the same directory.
- **Safe replace.** During the swap the original is renamed to a hidden
  `.<basename>.original` sidecar; it's only deleted after the encoded file is
  written and fsync'd. Same-filename cases (a `.mp4` with wrong codec) are
  handled correctly.
- **Probe cache.** ffprobe results are cached in SQLite keyed by
  `(path, size, mtime)`. Subsequent dispatch runs skip already-checked files
  without re-probing.
- **Retry budget.** Failed files retry up to `MAX_RETRIES` times across
  invocations; after that they're marked `failed_permanent` and skipped until
  you run `mediforge jobs retry <path>` or dispatch with `--force`.
- **Concurrency safe.** A lockfile prevents overlapping `dispatch` runs. The
  worker encodes one file at a time (returns 503 otherwise).

---

## Master setup (Unraid)

### 1. Configure environment

```bash
cp .env.example .env
```

Edit `.env` and set at minimum:
- `WORKER_URL` — e.g. `http://mac-mini.lan:8080`
- `WORKER_TOKEN` — `openssl rand -hex 32` (must match worker)
- `MEDIA_TV` / `MEDIA_MOVIES` — host paths for the volume mounts

### 2. Build and run

```bash
docker compose build
docker compose run --rm mediforge dispatch            # all libraries
docker compose run --rm mediforge dispatch tv         # one library
docker compose run --rm mediforge dispatch --dry-run  # what would happen
```

For periodic runs, add a host cron entry:

```
0 */6 * * * docker compose -f /path/to/docker-compose.yml run --rm mediforge dispatch >> /var/log/mediforge.log 2>&1
```

---

## Worker setup (Mac Mini)

See [`deploy/README.md`](deploy/README.md). Summary: `brew install ffmpeg`,
cross-compile `cmd/mediforge-worker` for `darwin/arm64`, drop the binary at
`/usr/local/bin/mediforge-worker`, install the launchd plist.

---

## Commands

```
mediforge dispatch [library...]       Encode files not already in target format
mediforge probe <file>                Probe a single file, print JSON
mediforge cache stats                 Row counts, jobs breakdown, DB size
mediforge cache evict <path>          Forget a file (probe + job rows)
mediforge jobs list [--status=STATUS] List jobs; STATUS = active|done|failed|failed_permanent
mediforge jobs retry <path>           Reset attempts to 0 and flip status to 'failed'
mediforge version
```

Dispatch flags: `--dry-run`, `--force`, `--max-retries=N`, `--log-level=LEVEL`.

Exit codes: `0` success, `1` partial failure, `2` config/startup error.

---

## Target output format

```
-c:v libx264 -preset medium -crf 20 (or h264_videotoolbox -b:v 4000k)
-profile:v high -level 4.1
-c:a aac -b:a 192k -ac 2
-c:s mov_text
-movflags +faststart
-map 0:v -map 0:a -map "0:s?"
```

Files already matching (container ∈ {mov, mp4, m4a}, video = h264 high, audio =
aac) are skipped.

---

## Environment reference

### Master

| Var | Required | Default | Purpose |
|---|---|---|---|
| `MEDIA_LIBRARIES` | yes | — | `tv:/media/tv,movies:/media/movies` |
| `WORKER_URL` | yes | — | `http://mac-mini.lan:8080` |
| `WORKER_TOKEN` | yes | — | shared secret |
| `ARCHIVE_MODE` | no | `replace` | only `replace` is implemented |
| `MAX_RETRIES` | no | `2` | attempts ceiling before `failed_permanent` |
| `MEDIFORGE_DB` | no | `/var/lib/mediforge/mediforge.db` | SQLite path |
| `HTTP_TIMEOUT_UPLOAD` | no | `30m` | per-file upload deadline |
| `HTTP_TIMEOUT_ENCODE` | no | `6h` | per-file total deadline |
| `MEDIA_EXTENSIONS` | no | `mkv,avi,mp4,mov,ts,wmv,flv,m4v,webm` | scan filter |
| `LOG_LEVEL` | no | `info` | |
| `LOG_FORMAT` | no | `text` | `text` or `json` |
| `JELLYFIN_INTEGRATION_ENABLED` | no | `false` | gate for refresh trigger |
| `JELLYFIN_URL` | if enabled | — | Jellyfin base URL |
| `JELLYFIN_API_KEY` | if enabled | — | from Jellyfin dashboard → API Keys |

### Worker

| Var | Required | Default | Purpose |
|---|---|---|---|
| `LISTEN_ADDR` | no | `:8080` | bind address |
| `WORKER_TOKEN` | yes | — | must match master |
| `WORK_DIR` | no | `$HOME/.mediforge/work` | temp I/O |
| `FFMPEG_BIN` | no | `ffmpeg` (PATH) | |
| `FFPROBE_BIN` | no | `ffprobe` (PATH) | |
| `ENCODER` | no | `auto` | `auto`/`videotoolbox`/`libx264` |
| `LOG_LEVEL` | no | `info` | |

---

## Migration from the bash version

The following env vars are no longer read and should be removed from `.env`:

```
WORKER_HOST, WORKER_USER, WORKER_INBOX, WORKER_OUTBOX, WORKER_ENCODE_SCRIPT,
SSH_KEY, SSH_KEY_PATH, MASTER_INBOX, JELLYFIN_LIBRARY, SETTLE_DELAY,
SCAN_INTERVAL, PIPELINE_PROFILE, ARCHIVE_ENABLED, DEBUG
```

The inbox/outbox folders on the worker (`~/media-pipeline/`) can be deleted.
SSH authorized_keys entries for the master can be removed.

---

## Troubleshooting

**"worker health check failed"**
Master can't reach the worker. Check `WORKER_URL`, worker is up
(`launchctl list | grep mediforge`), LAN reachable, token matches.

**Files skipped with `reason=permanent_failure`**
File reached `MAX_RETRIES`. Inspect with `mediforge jobs list --status=failed_permanent`,
then `mediforge jobs retry <path>` or run dispatch with `--force`.

**`another dispatch run is active`**
A prior run crashed holding the lockfile, or a genuine concurrent run exists.
The lockfile is at `$MEDIFORGE_DB.lock`. If no process is running, remove it.

**Encoded file failed to replace original**
The operation is atomic — either the final `.mp4` is in place or the
`.original` sidecar restored the source. If you see leftover
`.*.mediforge.tmp` files after a crash, they're safe to delete.
