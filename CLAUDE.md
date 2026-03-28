# Funnel — Developer Guide for Claude

This file provides context for AI assistants (and human contributors) working on the Funnel codebase.

---

## Project Summary

**Funnel** downloads BitTorrent content and streams it directly to S3-compatible object storage using a chunk-based multipart upload pipeline. Files are never permanently stored on disk — only a rolling buffer of at most 2 chunks (×10 MB) exists locally at any time.

**Module**: `github.com/gilang/funnel`
**Go version**: 1.22+
**Main branch**: `main`

---

## Project Structure

```
funnel/                         # root package "funnel" — S3 storage engine
  s3.go                         # S3Client interface, Config, S3Storage, NewS3Storage, DeleteTorrentData
  torrent.go                    # s3Torrent, s3Piece, read/write dispatch logic
  file.go                       # s3FileState, s3Chunk, multipart upload, GC
  piece.go                      # s3PieceCompletion, S3 piece markers
  storage_test.go               # 11 integration-style tests (package funnel)

storages/
  s3.go                         # S3Config (with credentials), NewS3Storage factory
  local.go                      # localStorageImpl wrapping anacrolix file storage + DeleteTorrentData

internal/daemon/
  types.go                      # Status, TorrentInfo, AddRequest/Response, ActionRequest, DaemonStatus, ErrorResponse
  state.go                      # SavedTorrent, State — JSON persistence to disk
  manager.go                    # Manager: torrent lifecycle, queue logic, watchTorrent goroutine
  server.go                     # HTTP server over IPC; managerIface; all route handlers
  state_test.go                 # State unit tests
  server_test.go                # Server handler tests (mock manager)

internal/ipc/
  ipc.go                        # SocketPath(), SetSocketPath() — platform-specific paths
  listener_unix.go              # NewListener() — Unix domain socket (!windows)
  listener_windows.go           # NewListener() — Named pipe (windows, go-winio)
  dialer_unix.go                # NewHTTPClient() — HTTP over unix socket (!windows)
  dialer_windows.go             # NewHTTPClient() — HTTP over named pipe (windows, go-winio)
  ipc_test.go                   # SocketPath tests

cmd/cli/
  main.go                       # entry point: calls cmd.Execute()
  cmd/root.go                   # Cobra root, Viper config init, --socket and --config flags
  cmd/client.go                 # apiClient(), apiURL(), apiBase — shared HTTP helpers
  cmd/daemon.go                 # `funnel daemon` — start IPC daemon in foreground
  cmd/start.go                  # `funnel start` — spawn daemon, wait for socket
  cmd/shutdown.go               # `funnel shutdown`
  cmd/status.go                 # `funnel status`
  cmd/add.go                    # `funnel add <magnet>`
  cmd/list.go                   # `funnel list [-d/-s/-p/-f/-q]`
  cmd/stop.go                   # `funnel stop <id>`
  cmd/pause.go                  # `funnel pause <id>`
  cmd/resume.go                 # `funnel resume <id>`
  cmd/remove.go                 # `funnel remove <id>`
  cmd/autostart.go              # `funnel autostart enable|disable` (Cobra command)
  cmd/autostart_darwin.go       # macOS: LaunchAgents plist + launchctl
  cmd/autostart_linux.go        # Linux: systemd user unit
  cmd/autostart_windows.go      # Windows: registry HKCU Run key
  cmd/autostart_other.go        # Other platforms: unsupported error
  cmd/spawn_unix.go             # spawnDaemon() with Setsid=true (!windows)
  cmd/spawn_windows.go          # spawnDaemon() with CREATE_NEW_PROCESS_GROUP (windows)
  cmd/commands_test.go          # CLI command tests (httptest server override)

docker-compose.yml              # MinIO local dev
Dockerfile                      # Container build
```

---

## Key Design Decisions

### IPC Transport (not TCP)
The daemon listens on a Unix domain socket / Named Pipe — no TCP port is opened. The HTTP protocol is used over the socket. URL base is `http://localhost` (host is ignored; the transport determines the connection).

Socket paths:
- macOS: `~/Library/Application Support/funnel/funnel.sock`
- Linux: `$XDG_RUNTIME_DIR/funnel.sock` → `~/.local/share/funnel/funnel.sock`
- Windows: `\\.\pipe\funnel`

### S3 Storage Engine (root package)
- `S3Storage.OpenTorrent()` creates an `s3Torrent` for each infoHash
- Each file in the torrent becomes an `s3FileState` with `N` chunks of `ChunkSize` bytes
- `s3Chunk.uploadIfComplete()` triggers once all pieces covering the chunk are marked complete
- `finalizeMultipart()` calls `CompleteMultipartUpload` then deletes the state JSON
- `gcLocalChunks()` evicts uploaded chunks once `activeChunks > localLimit` (default 2)
- `findFile()` uses binary search (`fileStarts []int64`) for O(log n) piece→file mapping
- `s3ReadCtx()` = 30s timeout; `s3WriteCtx()` = 5 min timeout

### Queue Logic (Manager)
- `maxActive` (default 3) limits concurrent downloads; configurable via config/env/flag
- `tryStart(id)` promotes `queued → downloading` if a slot is available
- `processQueue()` is called after any download completes, Stop, or Remove
- `watchTorrent(id, mt, initialStatus)` goroutine: waits for `GotInfo()`, calls `tryStart`, then polls every 5s for `downloading → seeding` transition

### Torrent States
```
queued → downloading → seeding
           │               │
           └──► paused ◄───┘
                   │
                   └──► queued (on resume)
```
- `Pause` (downloading/queued): `DisallowDataDownload()`
- `Pause` (seeding): `t.Drop()`
- `Resume`: re-add magnet + new `watchTorrent` goroutine (old goroutine exits on `StatusPaused` detection)
- `Stop`: disconnect from client (`t.Drop()`), remove from map+state, **data retained**
- `Remove`: same as Stop + `StorageRemover.DeleteTorrentData()`

### Lock Ordering
Always acquire in this order to prevent deadlocks:
1. `m.mu` (Manager RWMutex — protects `m.torrents` map)
2. `mt.mu` (managedTorrent Mutex — protects `mt.status`, `mt.t`, `mt.name`)

### State Persistence
Stored as JSON:
- macOS: `~/Library/Application Support/funnel/state.json`
- Linux: `~/.local/share/funnel/state.json`
- Windows: `%APPDATA%\funnel\state.json`

`SavedTorrent.Paused = true` → re-added on startup without starting download.

### StorageRemover Interface
Defined in `internal/daemon/manager.go`. Called by `Manager.Remove()`:
- `S3Storage` → lists and deletes all objects with prefix `{infoHash}/`
- `localStorageImpl` → `os.RemoveAll(dir/{infoHash})`

### Server testability
`Server.mgr` uses `managerIface` (not concrete `*Manager`), so tests can inject a `mockManager`.

### CLI testability
`cmd/client.go` exposes `apiBase` (var) and `httpClientOverride` (var) so tests can redirect to an `httptest.Server` without touching the IPC socket.

---

## REST API

All routes use HTTP over IPC socket.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/torrents` | Add torrent — body: `{"magnet":"..."}` |
| `GET` | `/api/torrents[?status=...]` | List torrents (optional filter) |
| `PATCH` | `/api/torrents/{id}` | Action — body: `{"action":"pause"\|"resume"}` |
| `POST` | `/api/torrents/{id}/stop` | Stop/disconnect (data kept) |
| `DELETE` | `/api/torrents/{id}` | Remove + delete data |
| `GET` | `/api/status` | Daemon status + per-state counts |
| `POST` | `/api/shutdown` | Graceful shutdown |

---

## Configuration (Viper)

| Key | Default | Env var | Description |
|-----|---------|---------|-------------|
| `storage.type` | `local` | `FUNNEL_STORAGE_TYPE` | `local` or `s3` |
| `storage.local.dir` | `~/Downloads/funnel` | `FUNNEL_STORAGE_LOCAL_DIR` | Local dir |
| `storage.s3.*` | — | `FUNNEL_STORAGE_S3_*` | S3 credentials and config |
| `upload-rate` | `524288` | `FUNNEL_UPLOAD_RATE` | Upload bytes/sec (0 = unlimited) |
| `max-active` | `3` | `FUNNEL_MAX_ACTIVE` | Max concurrent downloads |

Config file location: `~/.config/funnel/config.yaml`

---

## Build & Test

```bash
# Build all packages
go build ./...

# Run all tests with race detector
go test -race ./...

# Run a specific package
go test -race ./internal/daemon/

# Vet
go vet ./...

# Local MinIO for S3 tests
docker compose up -d
```

### Test Packages

| Package | Test File | What It Tests |
|---------|-----------|---------------|
| `funnel` (root) | `storage_test.go` | S3 storage engine, multipart upload, piece completion, GC |
| `internal/daemon` | `state_test.go` | State persistence (Add, Remove, Update, reload) |
| `internal/daemon` | `server_test.go` | All HTTP handlers via mock manager |
| `internal/ipc` | `ipc_test.go` | SocketPath, SetSocketPath, platform detection |
| `cmd/cli/cmd` | `commands_test.go` | CLI commands against httptest server |

---

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/anacrolix/torrent` | BitTorrent client library |
| `github.com/aws/aws-sdk-go-v2` | S3 SDK |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/spf13/viper` | Config management |
| `github.com/Microsoft/go-winio` | Named Pipe support on Windows |
| `golang.org/x/time/rate` | Upload rate limiter |
