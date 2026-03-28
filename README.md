# Funnel

[![CI](https://github.com/gilang/funnel/actions/workflows/ci.yml/badge.svg)](https://github.com/gilang/funnel/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/gilang/funnel)](https://github.com/gilang/funnel/releases/latest)
[![Coverage](https://codecov.io/gh/gilang/funnel/branch/main/graph/badge.svg)](https://codecov.io/gh/gilang/funnel)
[![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go&logoColor=white)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**Funnel** is a daemon that bridges the BitTorrent network and S3-compatible object storage. It downloads torrents piece-by-piece and streams each chunk directly to S3 via multipart upload — keeping only a minimal local disk buffer at any time.

## Why Funnel?

- **Minimal disk usage** — at most 2 chunks (×10 MB) live on disk simultaneously; the rest goes straight to S3.
- **Resumable** — multipart upload state and piece completion markers are persisted to S3, so interrupted downloads continue where they left off.
- **Seeding from S3** — after download completes, Funnel seeds directly from S3, so peers can download without any local data.
- **Queue management** — configurable concurrency limit; excess torrents wait in a queue and start automatically as slots open.
- **Full lifecycle** — add, pause, resume, stop, remove via a clean CLI.
- **Autostart** — native OS integration (launchd on macOS, systemd on Linux, registry on Windows).

---

## How It Works

```
magnet link
    │
    ▼
BitTorrent peers ──► piece data ──► local chunk buffer (≤20 MB)
                                          │
                                          ▼ (chunk verified)
                              S3 multipart upload
                                          │
                                          ▼
                              chunk deleted from disk
                                          │
                                (all chunks done)
                                          ▼
                              CompleteMultipartUpload
                                          │
                                          ▼
                              seed from S3 ──► peers
```

---

## Installation

### Build from Source

Requirements: Go 1.22+

```bash
git clone https://github.com/gilang/funnel.git
cd funnel
go build -o funnel ./cmd/cli
```

Move the binary to a directory in your `$PATH`:

```bash
mv funnel /usr/local/bin/
```

---

## Quick Start

### 1. Configure Storage

Create `~/.config/funnel/config.yaml`:

**Local storage** (default):
```yaml
storage:
  type: local
  local:
    dir: ~/Downloads/funnel
```

**S3 / MinIO**:
```yaml
storage:
  type: s3
  s3:
    bucket: my-bucket
    endpoint: https://s3.amazonaws.com   # or your MinIO URL
    access-key: AKIAIOSFODNN7EXAMPLE
    secret-key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
    region: us-east-1
    base-dir: downloads
```

### 2. Start the Daemon

```bash
funnel start
```

### 3. Add a Torrent

```bash
funnel add 'magnet:?xt=urn:btih:...'
```

### 4. Monitor Progress

```bash
funnel list
funnel status
```

---

## CLI Reference

```
funnel <command> [flags]
```

### Daemon Management

| Command | Description |
|---------|-------------|
| `funnel start` | Start the daemon in the background |
| `funnel shutdown` | Gracefully stop the running daemon |
| `funnel daemon` | Run daemon in the foreground (used internally by `start`) |
| `funnel status` | Show daemon status and per-state torrent counts |

### Torrent Lifecycle

| Command | Description |
|---------|-------------|
| `funnel add <magnet>` | Add a torrent (queued immediately) |
| `funnel list` | List all torrents |
| `funnel list -d` | Show only downloading |
| `funnel list -s` | Show only seeding |
| `funnel list -p` | Show only paused |
| `funnel list -q` | Show only queued |
| `funnel list -f` | Show only failed |
| `funnel pause <id>` | Pause a torrent (stays in list, resumable) |
| `funnel resume <id>` | Resume a paused torrent |
| `funnel stop <id>` | Disconnect torrent from client, remove from list (data kept) |
| `funnel remove <id>` | Remove torrent and delete all its data from storage |

### Autostart

```bash
funnel autostart enable    # register with OS init system
funnel autostart disable   # unregister
```

Platform support:
- **macOS** — LaunchAgent plist (`~/Library/LaunchAgents/`)
- **Linux** — systemd user unit (`~/.config/systemd/user/`)
- **Windows** — registry key (`HKCU\...\Run`)

---

## Torrent States

```
           add
            │
            ▼
         queued ──────────────────────────────────► stop
            │                                         (remove from list,
            │ slot available                           data kept)
            ▼
       downloading ──── pause ──► paused ──── resume ──┐
            │                                           │
            │ complete                                  │
            ▼                                           │
         seeding ──── pause ──► paused ◄───────────────┘
            │
            ▼ stop / remove
         (gone)
```

| State | Meaning |
|-------|---------|
| `queued` | Waiting for a download slot |
| `downloading` | Actively downloading |
| `seeding` | Upload complete, seeding from storage |
| `paused` | Inactive, stays in list |
| `failed` | Error during download |

---

## Configuration

All settings can be provided via:
1. Config file (`~/.config/funnel/config.yaml`)
2. Environment variables (prefix `FUNNEL_`, e.g. `FUNNEL_MAX_ACTIVE=5`)
3. CLI flags (where applicable)

| Key | Default | Description |
|-----|---------|-------------|
| `storage.type` | `local` | Storage backend: `local` or `s3` |
| `storage.local.dir` | `~/Downloads/funnel` | Local download directory |
| `storage.s3.bucket` | — | S3 bucket name |
| `storage.s3.endpoint` | — | S3 endpoint URL |
| `storage.s3.access-key` | — | S3 access key |
| `storage.s3.secret-key` | — | S3 secret key |
| `storage.s3.region` | `us-east-1` | S3 region |
| `storage.s3.base-dir` | `./downloads` | Key prefix inside the bucket |
| `upload-rate` | `524288` | Max upload speed in bytes/sec (0 = unlimited) |
| `max-active` | `3` | Max concurrent downloads |

Override socket path:

```bash
funnel --socket /tmp/my.sock start
```

---

## S3 Object Layout

```
{infoHash}/files/{name}                        # Final file (after multipart complete)
{infoHash}/files/{name}/{relPath}              # Multi-file torrent
{infoHash}/state/multipart/{fileHex}.json      # Multipart upload state (UploadID + ETags)
{infoHash}/state/{pieceIndex}                  # Piece completion marker
{infoHash}/metainfo.json                       # Torrent metainfo cache
```

---

## IPC Transport

Funnel uses a Unix domain socket (macOS/Linux) or Named Pipe (Windows) for CLI↔daemon communication. No TCP port is opened.

Default socket paths:
- **macOS**: `~/Library/Application Support/funnel/funnel.sock`
- **Linux**: `$XDG_RUNTIME_DIR/funnel.sock` (fallback: `~/.local/share/funnel/funnel.sock`)
- **Windows**: `\\.\pipe\funnel`

---

## Development

### Local MinIO

```bash
docker compose up -d
```

Default MinIO credentials (see `docker-compose.yml`):

| | |
|--|--|
| Endpoint | `http://localhost:9000` |
| Console | `http://localhost:9001` |
| Bucket | `funnel` |
| Access Key | `user` |
| Secret Key | `password` |

### Build & Test

```bash
go build ./...
go test -race ./...
```

### Run daemon in foreground

```bash
go run ./cmd/cli daemon
```

---

## Limits

- Maximum torrent size: **100 GB**
- Minimum chunk size: **5 MB** (S3 multipart minimum)
- Default chunk size: **10 MB**

---

## Contributing

Pull requests are welcome. For significant changes, please open an issue first to discuss the approach.

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Make changes with tests
4. Run `go test -race ./...` and `go vet ./...`
5. Submit a pull request

---

## License

MIT — see [LICENSE](LICENSE).
