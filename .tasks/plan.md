# Funnel Phase 3 — Implementation Plan

## Overview: 3 New Binaries

| Binary | cmd/ dir | Purpose | Size estimate |
|--------|----------|---------|---------------|
| `funneld` | `cmd/standalone/` | AIO: TCP HTTP API + local torrent Manager | ~30MB |
| `funnel-manager` | `cmd/manager/` | Cluster coordinator: TCP API + DB, NO torrent | ~15MB |
| `funnel-worker` | `cmd/worker/` | Download node: torrent client + HTTP client, NO DB | ~25MB |

Size savings from separate binaries: manager has NO anacrolix/torrent or aws-sdk-go.
Worker has NO DB drivers. CLI has none of the above.

---

## Full Target Folder Structure

```
cmd/
  cli/                      # existing — DO NOT TOUCH
  standalone/               # NEW — funneld binary
    main.go
    cmd/
      root.go               # --port (default 8080), --state (file|mysql|postgres), --db-dsn
      serve.go              # `funneld serve` — start AIO
  manager/                  # NEW — funnel-manager binary
    main.go
    cmd/
      root.go               # --port, --db-driver (mysql|postgres), --db-dsn
      serve.go              # `funnel-manager serve`
      token.go              # `funnel-manager token create/list/revoke`
  worker/                   # NEW — funnel-worker binary
    main.go
    cmd/
      root.go               # --manager URL, --token TOKEN, --capacity N
      run.go                # `funnel-worker run` (default command)

internal/
  daemon/                   # existing — MODIFY only statestore.go + manager.go
    statestore.go           # NEW: StateStore interface
    state.go                # MODIFY: *State implements StateStore (add 0 methods, just satisfy interface)
    manager.go              # MODIFY: change `state *State` field to `state StateStore`
    server.go               # existing — unchanged
    types.go                # existing — unchanged

  ipc/                      # existing — DO NOT TOUCH

  store/                    # NEW: DB layer using squirrel
    store.go                # Store interface
    jobs.go                 # JobRepository interface + Job, JobFilter, JobStatus types
    workers.go              # WorkerRepository interface + WorkerInfo types
    tokens.go               # TokenRepository interface + JoinToken types
    mysql.go                # NewMySQLStore(dsn string) (Store, error)
    mysql_jobs.go           # mysqlJobRepo — squirrel.Question placeholder
    mysql_workers.go        # mysqlWorkerRepo
    mysql_tokens.go         # mysqlTokenRepo
    postgres.go             # NewPostgresStore(dsn string) (Store, error)
    postgres_jobs.go        # pgJobRepo — squirrel.Dollar placeholder
    postgres_workers.go     # pgWorkerRepo
    postgres_tokens.go      # pgTokenRepo
    mysql_state.go          # mysqlStateStore implements daemon.StateStore
    postgres_state.go       # pgStateStore implements daemon.StateStore
    migrations/
      mysql/
        001_init.sql        # CREATE TABLE jobs, workers, join_tokens, state_torrents
      postgres/
        001_init.sql        # same but Postgres syntax

  cluster/                  # NEW: worker ↔ manager protocol
    types.go                # WorkerInfo, Job, JobResult, HeartbeatReq/Res, RegisterReq/Res
    coordinator.go          # Coordinator: worker pool, job queue, heartbeat monitor
    agent.go                # Agent: register loop + poll loop + execute
    token.go                # GenerateToken(), ValidateToken()

  provision/                # NEW: auto-scale interface (noop only for now)
    provisioner.go          # type Provisioner interface { Scale(ctx, n int) error }
    noop.go                 # NoopProvisioner{}
```

---

## Step-by-Step Implementation Order

### Step 1: StateStore interface (5 min)

**File**: `internal/daemon/statestore.go` (NEW)
```go
package daemon

// StateStore is the persistence backend for saved torrents.
// Implemented by *State (JSON file), mysqlStateStore, pgStateStore.
type StateStore interface {
    List() []SavedTorrent
    Add(t SavedTorrent) error
    Remove(id string) error
    Update(id string, fn func(*SavedTorrent)) error
}
```

**File**: `internal/daemon/manager.go` (MODIFY)
- Change field: `state *State` → `state StateStore`
- Change `NewManager` param: `st *State` → `st StateStore`
- Everything else unchanged — *State already has all 4 methods

**Verify**: `go build ./...` still passes.

---

### Step 2: DB Store Layer (biggest step)

**New dependency** — add to go.mod:
```
github.com/Masterminds/squirrel v1.5.4
github.com/go-sql-driver/mysql v1.8.1
github.com/lib/pq v1.10.9
```

#### DB Schema

**`internal/store/migrations/mysql/001_init.sql`**:
```sql
CREATE TABLE IF NOT EXISTS jobs (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    magnet       TEXT          NOT NULL,
    info_hash    VARCHAR(64)   NOT NULL UNIQUE,
    status       VARCHAR(32)   NOT NULL DEFAULT 'queued',
    worker_id    VARCHAR(64),
    name         VARCHAR(512),
    size         BIGINT        NOT NULL DEFAULT 0,
    progress     DOUBLE        NOT NULL DEFAULT 0,
    error_msg    TEXT,
    paused       TINYINT(1)    NOT NULL DEFAULT 0,
    created_at   DATETIME      NOT NULL,
    updated_at   DATETIME      NOT NULL,
    started_at   DATETIME,
    completed_at DATETIME
);

CREATE TABLE IF NOT EXISTS workers (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    address      VARCHAR(256)  NOT NULL,
    capacity     INT           NOT NULL DEFAULT 1,
    active_jobs  INT           NOT NULL DEFAULT 0,
    status       VARCHAR(32)   NOT NULL DEFAULT 'active',
    version      VARCHAR(64),
    last_seen    DATETIME      NOT NULL,
    joined_at    DATETIME      NOT NULL
);

CREATE TABLE IF NOT EXISTS join_tokens (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    token_hash   VARCHAR(128)  NOT NULL UNIQUE,
    name         VARCHAR(128),
    created_at   DATETIME      NOT NULL,
    expires_at   DATETIME,
    revoked      TINYINT(1)    NOT NULL DEFAULT 0
);

-- Used by standalone DB state (mirrors daemon.SavedTorrent)
CREATE TABLE IF NOT EXISTS state_torrents (
    id           VARCHAR(64)   NOT NULL PRIMARY KEY,
    magnet       TEXT          NOT NULL,
    name         VARCHAR(512),
    paused       TINYINT(1)    NOT NULL DEFAULT 0
);
```

**`internal/store/migrations/postgres/001_init.sql`**:
Same structure but:
- `TINYINT(1)` → `BOOLEAN`
- `DATETIME` → `TIMESTAMPTZ`
- `DOUBLE` → `DOUBLE PRECISION`
- `AUTO_INCREMENT` (not used here) → `SERIAL` (not needed)
- `VARCHAR(64) NOT NULL PRIMARY KEY` stays the same

#### Store Interface

**`internal/store/store.go`**:
```go
package store

// Store is the top-level interface combining all repositories.
type Store interface {
    Jobs() JobRepository
    Workers() WorkerRepository
    Tokens() TokenRepository
    Close() error
    // RunMigrations applies embedded SQL migrations.
    RunMigrations(ctx context.Context) error
}
```

**`internal/store/jobs.go`**:
```go
package store

import "context"

type JobStatus string
const (
    JobQueued      JobStatus = "queued"
    JobAssigned    JobStatus = "assigned"
    JobDownloading JobStatus = "downloading"
    JobSeeding     JobStatus = "seeding"
    JobPaused      JobStatus = "paused"
    JobFailed      JobStatus = "failed"
    JobDone        JobStatus = "done"
)

type Job struct {
    ID          string
    Magnet      string
    InfoHash    string
    Status      JobStatus
    WorkerID    string     // empty if unassigned
    Name        string
    Size        int64
    Progress    float64
    ErrorMsg    string
    Paused      bool
    CreatedAt   time.Time
    UpdatedAt   time.Time
    StartedAt   *time.Time
    CompletedAt *time.Time
}

type JobFilter struct {
    Status   JobStatus // empty = all
    WorkerID string    // empty = all
}

type JobRepository interface {
    Create(ctx context.Context, job *Job) error
    Get(ctx context.Context, id string) (*Job, error)
    GetByInfoHash(ctx context.Context, infoHash string) (*Job, error)
    Update(ctx context.Context, id string, fn func(*Job)) error
    List(ctx context.Context, filter JobFilter) ([]Job, error)
    Delete(ctx context.Context, id string) error
    // NextPending returns the oldest queued unassigned job, or nil.
    NextPending(ctx context.Context) (*Job, error)
}
```

**`internal/store/workers.go`**:
```go
package store

type WorkerInfo struct {
    ID         string
    Address    string
    Capacity   int
    ActiveJobs int
    Status     string    // "active" | "draining" | "offline"
    Version    string
    LastSeen   time.Time
    JoinedAt   time.Time
}

type WorkerRepository interface {
    Upsert(ctx context.Context, w *WorkerInfo) error
    Get(ctx context.Context, id string) (*WorkerInfo, error)
    List(ctx context.Context) ([]WorkerInfo, error)
    Remove(ctx context.Context, id string) error
    // MarkStale marks workers not seen in > threshold as offline.
    MarkStale(ctx context.Context, threshold time.Duration) error
}
```

**`internal/store/tokens.go`**:
```go
package store

type JoinToken struct {
    ID        string
    TokenHash string    // bcrypt or SHA256 of raw token
    Name      string
    CreatedAt time.Time
    ExpiresAt *time.Time
    Revoked   bool
}

type TokenRepository interface {
    Create(ctx context.Context, t *JoinToken) error
    GetByHash(ctx context.Context, hash string) (*JoinToken, error)
    List(ctx context.Context) ([]JoinToken, error)
    Revoke(ctx context.Context, id string) error
}
```

#### MySQL Implementation Pattern

**`internal/store/mysql.go`**:
```go
package store

import (
    "context"
    "database/sql"
    _ "github.com/go-sql-driver/mysql"
    sq "github.com/Masterminds/squirrel"
)

type mysqlStore struct {
    db   *sql.DB
    jobs *mysqlJobRepo
    wrk  *mysqlWorkerRepo
    tok  *mysqlTokenRepo
}

// Each sub-repo uses squirrel.Question (? placeholders for MySQL)
var mysqlBuilder = sq.StatementBuilder.PlaceholderFormat(sq.Question)

func NewMySQLStore(dsn string) (Store, error) { ... }
func (s *mysqlStore) Jobs() JobRepository    { return s.jobs }
func (s *mysqlStore) Workers() WorkerRepository { return s.wrk }
func (s *mysqlStore) Tokens() TokenRepository { return s.tok }
func (s *mysqlStore) Close() error           { return s.db.Close() }
func (s *mysqlStore) RunMigrations(ctx context.Context) error { /* embed+exec */ }
```

**`internal/store/postgres.go`**:
Same but:
- `_ "github.com/lib/pq"`
- `pgBuilder = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)`
- `sql.Open("postgres", dsn)`

#### StateStore Bridge for Standalone DB

**`internal/store/mysql_state.go`**:
```go
// mysqlStateStore implements daemon.StateStore using the jobs table's
// state_torrents table (separate simple table, not the full jobs table).
type mysqlStateStore struct{ db *sql.DB }

func NewMySQLStateStore(dsn string) (daemon.StateStore, error)
// List/Add/Remove/Update map to SELECT/INSERT/DELETE/UPDATE on state_torrents
```

**`internal/store/postgres_state.go`**: same but Postgres driver + Dollar placeholder.

---

### Step 3: Cluster Package

**`internal/cluster/types.go`**:
```go
package cluster

// RegisterReq is sent by worker on startup.
type RegisterReq struct {
    WorkerID string `json:"worker_id,omitempty"` // empty = new registration
    Address  string `json:"address"`             // worker's callback address (if push model, optional)
    Capacity int    `json:"capacity"`             // max concurrent downloads
    Version  string `json:"version"`
}
type RegisterRes struct {
    WorkerID string `json:"worker_id"`
    Token    string `json:"token,omitempty"` // session token if needed
}

// PollRes is returned by GET /internal/jobs/next
type PollRes struct {
    Job *JobAssignment `json:"job"` // null if no job available
}
type JobAssignment struct {
    JobID    string `json:"job_id"`
    Magnet   string `json:"magnet"`
    InfoHash string `json:"info_hash"`
}

// ProgressReq is body for POST /internal/jobs/{id}/progress
type ProgressReq struct {
    Progress float64 `json:"progress"` // 0–100
    Status   string  `json:"status"`   // "downloading" | "seeding"
    Name     string  `json:"name,omitempty"`
    Size     int64   `json:"size,omitempty"`
    Peers    int     `json:"peers,omitempty"`
}

// HeartbeatReq is body for POST /internal/workers/{id}/heartbeat
type HeartbeatReq struct {
    ActiveJobs int `json:"active_jobs"`
}
```

**`internal/cluster/token.go`**:
```go
package cluster

import "crypto/rand"

// GenerateToken returns a 32-byte hex token.
func GenerateToken() (string, error)

// HashToken returns SHA256 hex of raw token (store the hash, not the token).
func HashToken(raw string) string
```

**`internal/cluster/coordinator.go`**:
Coordinator is used by funnel-manager. It:
1. Exposes HTTP routes at `/internal/*`
2. Maintains worker pool (backed by store.WorkerRepository)
3. On new job: find worker with available slot, assign via DB
4. Runs background goroutine to mark stale workers (heartbeat timeout = 30s)
5. Runs background goroutine to reassign jobs from stale workers

```go
type Coordinator struct {
    store store.Store
    prov  provision.Provisioner
}

func NewCoordinator(s store.Store, p provision.Provisioner) *Coordinator

// RegisterRoutes adds /internal/* handlers to an existing mux.
func (c *Coordinator) RegisterRoutes(mux *http.ServeMux)

// Internal route handlers:
// POST /internal/workers/register
// POST /internal/workers/{id}/heartbeat
// DELETE /internal/workers/{id}
// GET /internal/jobs/next
// POST /internal/jobs/{id}/progress
// POST /internal/jobs/{id}/complete
// POST /internal/jobs/{id}/fail
```

**`internal/cluster/agent.go`**:
Agent is used by funnel-worker. It:
1. Registers with manager on start
2. Runs poll loop: GET /internal/jobs/next every 5s
3. On job received: start anacrolix torrent download via daemon.Manager
4. Reports progress every 10s
5. On completion: POST /internal/jobs/{id}/complete
6. Sends heartbeat every 15s

```go
type Agent struct {
    managerURL string
    token      string
    workerID   string
    mgr        *daemon.Manager  // local torrent manager
    client     *http.Client
}

func NewAgent(managerURL, token string, mgr *daemon.Manager) *Agent
func (a *Agent) Run(ctx context.Context) error
```

---

### Step 4: Standalone Binary (`cmd/standalone/`)

**`cmd/standalone/main.go`**:
```go
package main

import "github.com/gilang-as/funnel/cmd/standalone/cmd"
func main() { cmd.Execute() }
```

**`cmd/standalone/cmd/root.go`**:
- Cobra root command, Viper init (same config file as CLI)
- Flags: `--port` (default 8080), `--state` (file|mysql|postgres), `--db-dsn`
- On file state: use existing `daemon.LoadState()` → *State implements StateStore
- On db state: use `store.NewMySQLStateStore(dsn)` or `store.NewPostgresStateStore(dsn)`

**`cmd/standalone/cmd/serve.go`**:
```go
// Same logic as cmd/cli/cmd/daemon.go but:
// - Uses TCP listener instead of ipc.NewListener()
// - net.Listen("tcp", ":"+port)
// - daemon.NewServer() reused as-is (accepts net.Listener)
// - storageInfo built same way as CLI daemon
```

Key: `daemon.Server.ListenAndServe()` currently calls `ipc.NewListener()` internally.
We need to make it accept an external `net.Listener`.

**REQUIRED CHANGE to `internal/daemon/server.go`**:
```go
// Add a Serve method that accepts an external listener:
func (s *Server) Serve(ln net.Listener) error {
    log.Printf("[daemon] listening on %s", ln.Addr())
    return s.srv.Serve(ln)
}
// Keep ListenAndServe() for backward compat (CLI uses it):
func (s *Server) ListenAndServe() error {
    ln, err := ipc.NewListener()
    if err != nil { return err }
    return s.Serve(ln)
}
```

---

### Step 5: Manager Binary (`cmd/manager/`)

**`cmd/manager/cmd/root.go`**:
- Flags: `--port` (default 8080), `--db-driver` (mysql|postgres), `--db-dsn`
- db-dsn required (no file state in manager)
- Init store, run migrations, init coordinator

**`cmd/manager/cmd/serve.go`**:
```
1. Init store (MySQL or Postgres)
2. Run migrations
3. Init coordinator (store + NoopProvisioner)
4. Create managerIface that wraps coordinator (implements Add/List/Pause/Resume/Stop/Remove/DaemonStatus)
5. Start TCP HTTP server with:
   - Public routes from daemon.Server handlers (reused)
   - Internal routes from coordinator.RegisterRoutes()
   - Auth middleware on /internal/* (validates Bearer token)
```

The manager does NOT run anacrolix torrent client. It delegates to workers.
`managerIface` implementation in manager mode proxies to DB/coordinator:
- `Add(magnet)` → create job in DB
- `List(filter)` → query DB
- `Pause/Resume/Stop/Remove` → update job status in DB + notify worker (or worker picks it up on next poll)
- `DaemonStatus()` → count from DB + worker list

**`cmd/manager/cmd/token.go`**:
```
funnel-manager token create [--name NAME] [--ttl 24h]
  → generate token, store hash, print raw token once

funnel-manager token list
  → show all tokens (id, name, created, expires, revoked)

funnel-manager token revoke <id>
  → mark revoked in DB
```

---

### Step 6: Worker Binary (`cmd/worker/`)

**`cmd/worker/cmd/root.go`**:
- Flags: `--manager` URL (required), `--token` TOKEN (required, or env FUNNEL_JOIN_TOKEN),
  `--capacity` N (default 3), `--storage-type`, `--storage-*` (same as CLI)

**`cmd/worker/cmd/run.go`**:
```
1. Init storage (same as CLI daemon: local or S3)
2. Create daemon.Manager with StateStore = in-memory or noop
   (worker doesn't persist state independently; manager DB is source of truth)
3. Create cluster.Agent(managerURL, token, manager)
4. agent.Run(ctx)  // blocks until ctx cancelled
```

**Note on worker state**: Worker uses a transient StateStore (in-memory) since the manager
DB is the source of truth. Create `store.NewMemoryStateStore()` — a simple in-memory
implementation of `daemon.StateStore`. No persistence on worker restart; manager reassigns.

---

## Internal API: Manager ↔ Worker Routes

All require `Authorization: Bearer <join-token>`.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/internal/workers/register` | Worker joins cluster, gets worker-id |
| POST | `/internal/workers/{id}/heartbeat` | Keep-alive, report active_jobs count |
| DELETE | `/internal/workers/{id}` | Worker leaving gracefully |
| GET | `/internal/jobs/next` | Poll for next job (204 = nothing) |
| POST | `/internal/jobs/{id}/progress` | Report download progress |
| POST | `/internal/jobs/{id}/complete` | Job done (seeding) |
| POST | `/internal/jobs/{id}/fail` | Job failed + error message |

---

## Provision Interface (just define, no impl)

**`internal/provision/provisioner.go`**:
```go
package provision

import "context"

type Provisioner interface {
    // Scale sets target worker replica count.
    Scale(ctx context.Context, n int) error
    // CurrentReplicas returns current worker count.
    CurrentReplicas(ctx context.Context) (int, error)
}
```

**`internal/provision/noop.go`**:
```go
type NoopProvisioner struct{}
func (NoopProvisioner) Scale(ctx context.Context, n int) error { return nil }
func (NoopProvisioner) CurrentReplicas(ctx context.Context) (int, error) { return 0, nil }
```

---

## Makefile Additions

```makefile
build-standalone:
    go build $(LDFLAGS) -o funneld ./cmd/standalone

build-manager:
    go build $(LDFLAGS) -o funnel-manager ./cmd/manager

build-worker:
    go build $(LDFLAGS) -o funnel-worker ./cmd/worker

build-all: build build-standalone build-manager build-worker
```

---

## Verification Checklist

```bash
# Step 1 done:
go build ./...                    # no errors
go test -race ./internal/daemon/  # still passes

# Step 2 done:
go test -race ./internal/store/   # store tests pass

# Step 3 done:
go test -race ./internal/cluster/ # cluster tests pass

# Step 4 done:
go build ./cmd/standalone/
./funneld serve --port 8080
curl http://localhost:8080/api/status

# Step 5 done:
go build ./cmd/manager/
./funnel-manager serve --db-driver postgres --db-dsn "..."
./funnel-manager token create --name test

# Step 6 done:
go build ./cmd/worker/
./funnel-worker run --manager http://localhost:8080 --token <token>

# Full E2E:
./funnel-manager serve &
./funnel-worker run --manager http://localhost:8080 --token abc &
./funnel-manager token create   # get token
curl -X POST http://localhost:8080/api/torrents -d '{"magnet":"..."}'
curl http://localhost:8080/api/status
```

---

## Important Notes

1. **Do NOT modify `cmd/cli/`** — it's the existing IPC-based CLI and must remain unchanged.
2. **`daemon.Server`** needs a small change: add `Serve(ln net.Listener) error` method
   while keeping `ListenAndServe()` for backward compat.
3. **`daemon.Manager`** field change: `state *State` → `state StateStore` — this is a
   compile-time change, zero runtime difference for existing file-based state.
4. **Worker StateStore**: use an in-memory implementation (`store.NewMemoryStateStore()`)
   since workers don't own state persistence.
5. **Auth middleware**: `Bearer <join-token>` on `/internal/*` routes only. Public `/api/*`
   routes are unauthenticated (manager is typically behind a private network anyway).
6. **Squirrel placeholder**: MySQL uses `sq.Question` (?), Postgres uses `sq.Dollar` ($1).
   This is the ONLY difference between mysql_*.go and postgres_*.go implementations.
7. **Migrations**: embed SQL files with `//go:embed migrations/mysql/*.sql` and run them
   on store init. Use sequential numbered files (001_init.sql, 002_*.sql, etc.).
