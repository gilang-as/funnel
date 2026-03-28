# Funnel — Developer Guide for Claude

This file provides context for AI assistants (and human contributors) working on the Funnel codebase.

---

## Project Summary

**Funnel** downloads BitTorrent content and streams it directly to S3-compatible object storage using a chunk-based multipart upload pipeline. Files are never permanently stored on disk — only a rolling buffer of at most 2 chunks (×10 MB) exists locally at any time.

**Module**: `gopkg.gilang.dev/funnel`
**GitHub repo**: `github.com/gilang-as/funnel`
**Go version**: 1.26 (go.mod), minimum 1.22
**Main branch**: `main`

---

## Deployment Modes

| Mode | Binary | Transport | State |
|------|--------|-----------|-------|
| CLI daemon | `funnel` (cmd/cli) | Unix socket / Named Pipe (IPC) | JSON file |
| Standalone | `funneld` (cmd/standalone) | TCP | JSON file / MySQL / Postgres |
| Cluster | `funnel-manager` + `funnel-worker` | TCP | MySQL / Postgres (shared) |

---

## Project Structure

```
funnel/                         # root package "funnel" — S3 storage engine
  s3.go                         # S3Client interface, Config, S3Storage, NewS3Storage, DeleteTorrentData
  torrent.go                    # s3Torrent, s3Piece, read/write dispatch logic
  file.go                       # s3FileState, s3Chunk, multipart upload, GC
  piece.go                      # s3PieceCompletion, S3 piece markers
  storage_test.go               # integration-style tests (package funnel)

storages/
  s3.go                         # S3Config (with credentials), NewS3Storage factory
  local.go                      # localStorageImpl wrapping anacrolix file storage + DeleteTorrentData

internal/daemon/
  types.go                      # Status, TorrentInfo, AddRequest/Response, ActionRequest, DaemonStatus, StorageInfo, ErrorResponse
  state.go                      # SavedTorrent, State — JSON persistence to disk
  statestore.go                 # StateStore interface (file/MySQL/Postgres/memory implementations)
  manager.go                    # Manager: torrent lifecycle, queue logic, watchTorrent goroutine, StorageRemover interface
  server.go                     # HTTP server; managerIface; RegisterRoutes; NewServerCustom; all route handlers
  state_test.go                 # State unit tests
  server_test.go                # Server handler tests (mock manager)

internal/ipc/
  ipc.go                        # SocketPath(), SetSocketPath() — platform-specific paths
  listener_unix.go              # NewListener() — Unix domain socket (!windows)
  listener_windows.go           # NewListener() — Named pipe (windows, go-winio)
  dialer_unix.go                # NewHTTPClient() — HTTP over unix socket (!windows)
  dialer_windows.go             # NewHTTPClient() — HTTP over named pipe (windows, go-winio)
  ipc_test.go                   # SocketPath tests

internal/cluster/               # Cluster coordinator: Coordinator, Agent, token generation/hashing
internal/store/                 # Database store: Store interface, MySQL/Postgres/memory implementations

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
  cmd/version.go                # `funnel --version` (Version var set by ldflags)
  cmd/autostart.go              # `funnel autostart enable|disable` (Cobra command)
  cmd/autostart_darwin.go       # macOS: LaunchAgents plist + launchctl
  cmd/autostart_linux.go        # Linux: systemd user unit
  cmd/autostart_windows.go      # Windows: registry HKCU Run key
  cmd/autostart_other.go        # Other platforms: unsupported error
  cmd/spawn_unix.go             # spawnDaemon() with Setsid=true (!windows)
  cmd/spawn_windows.go          # spawnDaemon() with CREATE_NEW_PROCESS_GROUP (windows)
  cmd/commands_test.go          # CLI command tests (httptest server override)

cmd/standalone/
  main.go                       # entry point: calls cmd.Execute() → funneld
  cmd/root.go                   # --port, --state, --db-dsn flags; Viper init
  cmd/serve.go                  # `funneld serve` — TCP daemon; buildStorage(), buildStateStore()

cmd/manager/
  main.go                       # entry point: funnel-manager
  cmd/root.go                   # --port, --db-driver, --db-dsn flags
  cmd/serve.go                  # `funnel-manager serve` — cluster coordinator; dbManager; RegisterRoutes + /internal/ with auth
  cmd/token.go                  # `funnel-manager token create|list|revoke`

cmd/worker/
  main.go                       # entry point: funnel-worker
  cmd/root.go                   # --manager, --token, --capacity, --storage-* flags
  cmd/run.go                    # `funnel-worker run` — cluster agent; cluster.NewAgent

docker-compose.yml              # MinIO local dev
Dockerfile                      # Multi-binary image (ARG CMD=standalone|manager|worker, ARG VERSION)
```

---

## Key Design Decisions

### IPC Transport (CLI daemon only)
The `funnel` CLI daemon listens on a Unix domain socket / Named Pipe — no TCP port is opened. The HTTP protocol is used over the socket. URL base is `http://localhost` (host is ignored; the transport determines the connection).

Socket paths:
- macOS: `~/Library/Application Support/funnel/funnel.sock`
- Linux: `$XDG_RUNTIME_DIR/funnel.sock` → `~/.local/share/funnel/funnel.sock`
- Windows: `\\.\pipe\funnel`

`funneld` and `funnel-manager` listen on TCP (`--port 8080`).

### S3 Storage Engine (root package)
- `S3Storage.OpenTorrent()` creates an `s3Torrent` for each infoHash
- Each file in the torrent becomes an `s3FileState` with `N` chunks of `ChunkSize` bytes
- Chunk size auto-adjusts if file exceeds 10,000 parts; minimum non-final part size is 5 MB (`s3MinPartSize = 5<<20`) — S3 rejects smaller parts with `EntityTooSmall`
- `s3Chunk.uploadIfComplete()` triggers once all pieces covering the chunk are marked complete
- `finalizeMultipart()` calls `CompleteMultipartUpload` then deletes the state JSON
- `gcLocalChunks()` evicts uploaded chunks once `activeChunks > localLimit` (default 2); called on **both success and failure** paths of `doUpload()`
- AWS SDK v2 returns pointer types (`*bool`, `*int64`, etc.) — always use `aws.ToBool()` / `aws.ToInt64()` helpers, never dereference directly (may be nil on empty responses)
- `findFile()` uses binary search (`fileStarts []int64`) for O(log n) piece→file mapping
- `s3ReadCtx()` = 30s timeout; `s3WriteCtx()` = 5 min timeout
- `retryDelay` is an `atomic.Value` wrapping `func(attempt int) time.Duration` — thread-safe swap in tests

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
JSON file:
- macOS: `~/Library/Application Support/funnel/state.json`
- Linux: `~/.local/share/funnel/state.json`
- Windows: `%APPDATA%\funnel\state.json`

`SavedTorrent.Paused = true` → re-added on startup without starting download.

`StateStore` interface (in `statestore.go`) abstracts over: JSON file, MySQL, Postgres, in-memory (worker). Includes `Close() error` — MySQL/Postgres implementations close the underlying `*sql.DB`; file and memory implementations return nil. Always call `defer st.Close()` after obtaining a StateStore.

### Cluster Architecture
- `funnel-manager` uses a SQL database (MySQL or Postgres) as shared state
- `cluster.Coordinator` handles job distribution; exposes `/internal/` routes protected by Bearer token auth
- `funnel-worker` runs `cluster.Agent` which polls the manager, claims jobs, runs them via `daemon.Manager`, reports progress
- `claimJob` capacity check counts **only `downloading` and `queued` torrents** toward `capacity` — seeding jobs do not consume a slot
- Join tokens are hashed (SHA-256) before storage; raw token shown only once on creation
- `dbManager` in `cmd/manager/cmd/serve.go` bridges `daemon.managerIface` to SQL store

### Server Design
- `daemon.NewServer(mgr, cancel)` — standard IPC server for CLI and standalone
- `daemon.NewServerCustom(mgr, cancel)` — exposes `RegisterRoutes(mux)` for embedding in manager's mux
- `Server.mgr` uses `managerIface` (not concrete `*Manager`), so tests can inject a `mockManager`

### CLI testability
`cmd/client.go` exposes `apiBase` (var) and `httpClientOverride` (var) so tests can redirect to an `httptest.Server` without touching the IPC socket.

### Build: No CGO
All binaries build with `CGO_ENABLED=0`. No `import "C"` anywhere. `go-winio` uses pure-Go Windows syscalls. `build-race` intentionally omits `CGO_ENABLED=0` since the race detector requires CGO.

### Binary Size
ldflags `-s -w` strips symbol table and DWARF debug info. Applied in `Makefile`, `release.yml`, and `Dockerfile`.

---

## REST API

All routes use HTTP (over IPC socket for CLI, over TCP for standalone/manager).

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/torrents` | Add torrent — body: `{"magnet":"..."}` |
| `GET` | `/api/torrents[?status=...]` | List torrents (optional filter) |
| `PATCH` | `/api/torrents/{id}` | Action — body: `{"action":"pause"\|"resume"}` |
| `POST` | `/api/torrents/{id}/stop` | Stop/disconnect (data kept) |
| `DELETE` | `/api/torrents/{id}` | Remove + delete data |
| `GET` | `/api/status` | Daemon status + per-state counts + storage info |
| `POST` | `/api/shutdown` | Graceful shutdown |

Manager-only internal routes (Bearer token required):
- `POST /internal/workers/register` — worker registers on startup
- `POST /internal/workers/{id}/heartbeat` — worker heartbeat
- `DELETE /internal/workers/{id}` — worker graceful leave
- `POST /internal/jobs/claim` — worker atomically claims the next queued job
- `POST /internal/jobs/{id}/progress` — worker reports progress
- `POST /internal/jobs/{id}/complete` — worker marks job done (seeding)
- `POST /internal/jobs/{id}/requeue` — worker requeues job on shutdown
- `POST /internal/jobs/{id}/fail` — worker reports failure

---

## Configuration (Viper)

| Key | Default | Env var | Description |
|-----|---------|---------|-------------|
| `storage.type` | `local` | `FUNNEL_STORAGE_TYPE` | `local` or `s3` |
| `storage.local.dir` | `~/Downloads/funnel` | `FUNNEL_STORAGE_LOCAL_DIR` | Local dir |
| `storage.s3.endpoint` | — | `FUNNEL_STORAGE_S3_ENDPOINT` | S3 endpoint |
| `storage.s3.bucket` | — | `FUNNEL_STORAGE_S3_BUCKET` | S3 bucket |
| `storage.s3.access-key` | — | `FUNNEL_STORAGE_S3_ACCESS_KEY` | S3 access key |
| `storage.s3.secret-key` | — | `FUNNEL_STORAGE_S3_SECRET_KEY` | S3 secret key |
| `storage.s3.region` | `us-east-1` | `FUNNEL_STORAGE_S3_REGION` | S3 region |
| `storage.s3.base-dir` | `downloads` | `FUNNEL_STORAGE_S3_BASE_DIR` | Key prefix |
| `upload-rate` | `524288` | `FUNNEL_UPLOAD_RATE` | Upload bytes/sec (0 = unlimited) |
| `max-active` | `3` | `FUNNEL_MAX_ACTIVE` | Max concurrent downloads |
| `port` | `8080` | `FUNNEL_PORT` | TCP port (standalone/manager) |
| `state` | `file` | `FUNNEL_STATE` | State store: file/mysql/postgres |
| `db-dsn` | — | `FUNNEL_DB_DSN` | Database DSN |
| `manager` | `http://localhost:8080` | `FUNNEL_MANAGER` | Manager URL (worker) |
| `token` | — | `FUNNEL_JOIN_TOKEN` | Cluster join token (worker) |
| `capacity` | `3` | `FUNNEL_CAPACITY` | Max jobs per worker |

Config file: `~/.config/funnel/config.yaml`

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

## CI / CD (GitHub Actions)

| Workflow | File | Trigger | What it does |
|----------|------|---------|--------------|
| CI | `.github/workflows/ci.yml` | push/PR to main (Go/mod files) | Build + vet + test (Linux, macOS); cross-compile Windows; coverage upload |
| Release | `.github/workflows/release.yml` | GitHub Release published | Build 7 static binaries (funnel: 5 platforms, funneld: Linux amd64+arm64); upload to release |
| Images | `.github/workflows/images.yml` | GitHub Release published | Build + push Docker images (standalone, manager, worker) to Docker Hub with GHA cache |

Release binary naming: `{funnel|funneld}-{os}-{arch}[.exe]`

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
| `github.com/go-sql-driver/mysql` | MySQL driver (cluster state) |
| `github.com/lib/pq` | Postgres driver (cluster state) |
| `github.com/google/uuid` | UUID generation (cluster job IDs, token IDs) |
| `github.com/Masterminds/squirrel` | SQL query builder |
