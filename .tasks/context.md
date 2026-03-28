# Funnel — Full Project Context

## What Is Funnel

**Funnel** downloads BitTorrent content and streams it directly to S3-compatible storage
using chunk-based multipart upload. Files are never permanently on disk — only ≤2 chunks
(×10 MB) live locally at any time.

**Module**: `github.com/gilang/funnel`
**Go version**: 1.26
**Main branch**: `main`

---

## What Already Exists (Phase 0–2, COMPLETE)

### Root Package `funnel/`
S3 storage engine:
- `s3.go` — S3Client interface, Config, S3Storage, NewS3Storage, DeleteTorrentData
- `torrent.go` — s3Torrent, s3Piece, read/write dispatch logic
- `file.go` — s3FileState, s3Chunk, multipart upload, GC
- `piece.go` — s3PieceCompletion, S3 piece markers
- `storage_test.go` — 11 integration tests

### `storages/`
- `s3.go` — S3Config (with credentials), NewS3Storage factory
- `local.go` — localStorageImpl wrapping anacrolix file storage + DeleteTorrentData

### `internal/daemon/` — Core daemon logic
- `types.go` — Status (queued/downloading/seeding/paused/failed), TorrentInfo,
  AddRequest/Response, ActionRequest, DaemonStatus, StorageInfo, ErrorResponse
- `state.go` — SavedTorrent (with Paused field), State (JSON persistence)
- `manager.go` — Manager: managedTorrent, queue logic (maxActive=3), watchTorrent goroutine,
  resolveID (prefix matching), StorageRemover interface
- `server.go` — HTTP server over IPC (ipc.NewListener), managerIface, all route handlers

### `internal/ipc/` — IPC transport
- `ipc.go` — SocketPath(), SetSocketPath()
- `listener_unix.go` / `listener_windows.go` — NewListener()
- `dialer_unix.go` / `dialer_windows.go` — NewHTTPClient()

### `cmd/cli/` — `funnel` binary (IPC CLI, local only)
Commands: daemon, start, shutdown, status, add, list, pause, resume, stop, remove,
autostart (enable/disable), version

---

## Architecture Decisions (Already Made)

### IPC Transport (CLI)
- `funnel` CLI uses Unix socket (macOS/Linux) or Named Pipe (Windows) — NO TCP
- Socket paths: macOS `~/Library/Application Support/funnel/funnel.sock`,
  Linux `$XDG_RUNTIME_DIR/funnel.sock`, Windows `\\.\pipe\funnel`
- HTTP protocol over the socket; URL base `http://localhost`

### REST API (IPC-based, existing)
```
POST   /api/torrents              add torrent → {id, status, new}
GET    /api/torrents[?status=...] list with optional filter
PATCH  /api/torrents/{id}         body: {action: "pause"|"resume"}
POST   /api/torrents/{id}/stop    stop, data retained
DELETE /api/torrents/{id}         remove + delete data
GET    /api/status                DaemonStatus (running, counts, storage info)
POST   /api/shutdown              graceful shutdown
```

### Torrent States & Queue
```
queued → downloading → seeding
           │               │
           └──► paused ◄───┘  (resume → back to queued → watchTorrent)
```
- maxActive = 3 (configurable). processQueue() after stop/remove/complete.
- watchTorrent goroutine: waits GotInfo(), tryStart(), polls every 5s.

### State Persistence (existing)
- macOS: `~/Library/Application Support/funnel/state.json`
- Linux: `~/.local/share/funnel/state.json`
- SavedTorrent.Paused = true → re-added without starting on daemon restart

### Lock Order (important — prevents deadlock)
1. `m.mu` (Manager RWMutex — protects m.torrents map)
2. `mt.mu` (managedTorrent Mutex — protects mt.status, mt.t, mt.name)

### StorageRemover Interface
In `internal/daemon/manager.go`. Called by Manager.Remove():
- S3Storage → lists+deletes all objects with prefix `{infoHash}/`
- localStorageImpl → os.RemoveAll(dir/{infoHash})

### Short-ID Resolution
`Manager.resolveID(id)` in manager.go: exact match first, then prefix scan,
error if ambiguous. All Pause/Resume/Stop/Remove use resolveID.

---

## Config (Viper, ~/.config/funnel/config.yaml)
```yaml
storage:
  type: local           # or s3
  local:
    dir: ~/Downloads/funnel
  s3:
    bucket: my-bucket
    endpoint: https://...
    access-key: ...
    secret-key: ...
    region: us-east-1
    base-dir: downloads
upload-rate: 524288     # bytes/sec (512KB default)
max-active: 3
```
Env prefix: `FUNNEL_*`

---

## Current go.mod Dependencies
```
github.com/anacrolix/torrent v1.61.0
github.com/aws/aws-sdk-go-v2 v1.41.5 (+ submodules)
github.com/Microsoft/go-winio v0.6.2
github.com/spf13/cobra v1.10.2
github.com/spf13/viper v1.21.0
golang.org/x/time v0.14.0
```
**NOT YET added** (needed for Phase 3):
- `github.com/Masterminds/squirrel` — SQL builder
- `github.com/go-sql-driver/mysql` — MySQL driver
- `github.com/lib/pq` — Postgres driver

---

## What Needs to Be Built (Phase 3)

See `plan.md` for full details. Summary:
1. **`funneld`** (`cmd/standalone/`) — AIO: HTTP API on TCP + local Manager
2. **`funnel-manager`** (`cmd/manager/`) — Cluster manager: API + DB + worker coordinator
3. **`funnel-worker`** (`cmd/worker/`) — Worker node: registers with manager, runs torrent client

New internal packages needed:
- `internal/daemon/statestore.go` — StateStore interface (tiny refactor)
- `internal/store/` — DB layer (squirrel, MySQL + Postgres)
- `internal/cluster/` — worker ↔ manager protocol
- `internal/provision/` — Provisioner interface (noop impl only for now)
