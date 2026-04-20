# Worker deployment (Mac Mini M2)

The worker runs as a native launchd agent — no Docker. Install:

## 1. Install ffmpeg

```
brew install ffmpeg
```

Verify the encoder list contains `h264_videotoolbox` (Apple Silicon GPU path):

```
ffmpeg -hide_banner -encoders | grep videotoolbox
```

## 2. Build the worker binary

From a dev machine:

```
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" \
  -o mediforge-worker ./cmd/mediforge-worker
scp mediforge-worker <user>@<mac-mini>:/usr/local/bin/
```

On an Intel Mac use `GOARCH=amd64`.

## 3. Create the runtime user and directories

Optional: dedicate a user for the worker. Otherwise use your own account and
adjust the paths in the plist.

```
sudo mkdir -p /Users/mediforge/.mediforge/work /Users/mediforge/Library/Logs
sudo chown -R mediforge /Users/mediforge/.mediforge /Users/mediforge/Library/Logs
```

## 4. Install the launchd plist

Copy `launchd/tech.enea.mediforge-worker.plist` to
`~/Library/LaunchAgents/tech.enea.mediforge-worker.plist`, then edit:

- `WORKER_TOKEN` — set to the same secret you put in the master's `.env` under
  `WORKER_TOKEN`. Generate with `openssl rand -hex 32`.
- `WORK_DIR` — where temp input/output files live. Ensure it's on a disk with
  ≥2× the largest single file you expect to process.
- Log paths — point at a writable directory for the runtime user.

Load it:

```
launchctl load ~/Library/LaunchAgents/tech.enea.mediforge-worker.plist
launchctl start tech.enea.mediforge-worker
```

## 5. Smoke test

From the master side (or any machine on the LAN):

```
curl -s http://<mac-mini>:8080/health
# {"ok":true,"busy":false,"version":"dev"}
```

## Environment variables

| Var | Default | Purpose |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | HTTP bind address |
| `WORKER_TOKEN` | — | shared secret (required) |
| `WORK_DIR` | `$HOME/.mediforge/work` | temp I/O |
| `FFMPEG_BIN` | `ffmpeg` | resolved via PATH |
| `FFPROBE_BIN` | `ffprobe` | resolved via PATH |
| `ENCODER` | `auto` | `auto`/`videotoolbox`/`libx264` |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `LOG_FORMAT` | `text` | `text` or `json` |

## Logs

- stdout → `StandardOutPath` (structured INFO)
- stderr → `StandardErrorPath` (slog output + ffmpeg stderr on error)

## Uninstall

```
launchctl unload ~/Library/LaunchAgents/tech.enea.mediforge-worker.plist
rm ~/Library/LaunchAgents/tech.enea.mediforge-worker.plist
sudo rm /usr/local/bin/mediforge-worker
```
