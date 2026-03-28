# Codebase Snapshot — Key File Signatures

This file contains the key signatures and patterns from existing code
that new code must integrate with.

---

## `internal/daemon/types.go` — Types

```go
type Status string
const (
    StatusQueued      Status = "queued"
    StatusDownloading Status = "downloading"
    StatusSeeding     Status = "seeding"
    StatusPaused      Status = "paused"
    StatusFailed      Status = "failed"
)

type StorageInfo struct {
    Type     string `json:"type"`
    Location string `json:"location"`
}

type DaemonStatus struct {
    Running bool           `json:"running"`
    Counts  map[Status]int `json:"counts"`
    Storage StorageInfo    `json:"storage"`
}

type TorrentInfo struct {
    ID       string  `json:"id"`
    Name     string  `json:"name"`
    Magnet   string  `json:"magnet"`
    Size     int64   `json:"size"`
    Progress float64 `json:"progress"`
    Status   Status  `json:"status"`
    Peers    int     `json:"peers"`
}

type AddRequest  struct { Magnet string `json:"magnet"` }
type AddResponse struct { ID string `json:"id"`; Status Status `json:"status"`; New bool `json:"new"` }
type ActionRequest struct { Action string `json:"action"` }
type ErrorResponse struct { Error string `json:"error"` }
```

---

## `internal/daemon/state.go` — State (JSON persistence)

```go
type SavedTorrent struct {
    ID     string `json:"id"`
    Magnet string `json:"magnet"`
    Name   string `json:"name,omitempty"`
    Paused bool   `json:"paused,omitempty"`
}

type State struct {
    path     string
    mu       sync.Mutex
    Torrents []SavedTorrent `json:"torrents"`
}

func LoadState(path string) (*State, error)
func (s *State) List() []SavedTorrent
func (s *State) Add(t SavedTorrent) error
func (s *State) Remove(id string) error
func (s *State) Update(id string, fn func(*SavedTorrent)) error
```

Note: After adding `internal/daemon/statestore.go`, `*State` will satisfy `StateStore`
interface without any code changes (it already has all 4 methods).

---

## `internal/daemon/manager.go` — Manager

```go
// StorageRemover is implemented by storage backends that support data deletion.
type StorageRemover interface {
    DeleteTorrentData(ctx context.Context, infoHash string) error
}

type managedTorrent struct {
    t      *torrent.Torrent
    magnet string
    name   string
    mu     sync.Mutex
    status Status
}

type Manager struct {
    client      *torrent.Client
    torrents    map[string]*managedTorrent
    mu          sync.RWMutex
    state       *State          // ← CHANGE THIS to StateStore after step 1
    stor        storage.ClientImpl
    maxActive   int
    storageInfo StorageInfo
}

// Constructor — CHANGE st *State to st StateStore after step 1
func NewManager(stor storage.ClientImpl, uploadRate int64, maxActive int, st *State, si StorageInfo) (*Manager, error)

func (m *Manager) Close()
func (m *Manager) Add(magnet string) (AddResponse, error)
func (m *Manager) Pause(id string) error      // uses resolveID internally
func (m *Manager) Resume(id string) error     // uses resolveID internally
func (m *Manager) Stop(id string) error       // uses resolveID internally
func (m *Manager) Remove(id string) error     // uses resolveID internally, calls StorageRemover
func (m *Manager) List(filter Status) []TorrentInfo
func (m *Manager) DaemonStatus() DaemonStatus
func (m *Manager) resolveID(id string) (string, error)  // prefix matching
```

---

## `internal/daemon/server.go` — HTTP Server

```go
// managerIface is the interface used by Server — both *Manager and mock satisfy it.
type managerIface interface {
    Add(magnet string) (AddResponse, error)
    List(filter Status) []TorrentInfo
    Pause(id string) error
    Resume(id string) error
    Stop(id string) error
    Remove(id string) error
    DaemonStatus() DaemonStatus
}

type Server struct {
    mgr    managerIface
    cancel context.CancelFunc
    srv    *http.Server
}

func NewServer(mgr *Manager, cancel context.CancelFunc) *Server

// CURRENT (IPC only):
func (s *Server) ListenAndServe() error  // calls ipc.NewListener() internally

// ADD THIS for standalone/manager TCP support:
func (s *Server) Serve(ln net.Listener) error  // accepts external listener

func (s *Server) Shutdown(ctx context.Context) error
```

Routes registered in NewServer:
- POST   /api/torrents
- GET    /api/torrents
- PATCH  /api/torrents/{id}
- POST   /api/torrents/{id}/stop
- DELETE /api/torrents/{id}
- GET    /api/status
- POST   /api/shutdown

---

## `internal/ipc/ipc.go` — Socket Path

```go
func SocketPath() string       // OS-specific socket path
func SetSocketPath(p string)   // override (for --socket flag)
```

---

## `storages/local.go` — Local Storage

```go
type localStorageImpl struct {
    storage.ClientImpl
    dir string
}

func (l *localStorageImpl) DeleteTorrentData(ctx context.Context, infoHash string) error
func NewLocalStorage(dir string) storage.ClientImpl
```

---

## `storages/s3.go` — S3 Storage Factory

```go
type S3Config struct {
    Bucket    string
    Endpoint  string
    AccessKey string
    SecretKey string
    Region    string
    BaseDir   string
}

func NewS3Storage(cfg S3Config) (storage.ClientImpl, error)
```

---

## `cmd/cli/cmd/daemon.go` — How Daemon Is Started (reference for standalone)

```go
// buildStorageInfo constructs StorageInfo from Viper config
func buildStorageInfo() daemon.StorageInfo {
    storType := viper.GetString("storage.type")
    if storType == "s3" {
        endpoint := strings.TrimRight(viper.GetString("storage.s3.endpoint"), "/")
        bucket := viper.GetString("storage.s3.bucket")
        baseDir := strings.Trim(viper.GetString("storage.s3.base-dir"), "/")
        loc := endpoint + "/" + bucket
        if baseDir != "" && baseDir != "." { loc += "/" + baseDir }
        return daemon.StorageInfo{Type: "s3", Location: loc}
    }
    dir := viper.GetString("storage.local.dir")
    if len(dir) > 1 && dir[:2] == "~/" {
        home, _ := os.UserHomeDir()
        dir = filepath.Join(home, dir[2:])
    }
    return daemon.StorageInfo{Type: "local", Location: dir}
}

// runDaemon is the core function called by `funnel daemon` command
func runDaemon(cmd *cobra.Command, args []string) {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    // 1. Build storage
    storType := viper.GetString("storage.type")
    var stor storage.ClientImpl
    // ... init local or S3 storage ...

    // 2. Build state path (OS-specific)
    statePath := filepath.Join(dataDir(), "state.json")
    st, _ := daemon.LoadState(statePath)

    // 3. Create manager
    uploadRate := viper.GetInt64("upload-rate")
    maxActive := viper.GetInt("max-active")
    mgr, _ := daemon.NewManager(stor, uploadRate, maxActive, st, buildStorageInfo())
    defer mgr.Close()

    // 4. Start server
    ctx2, cancel := context.WithCancel(ctx)
    defer cancel()
    srv := daemon.NewServer(mgr, cancel)
    go srv.ListenAndServe()

    // 5. Wait for shutdown
    <-ctx2.Done()
    shutCtx, _ := context.WithTimeout(context.Background(), 10*time.Second)
    srv.Shutdown(shutCtx)
}
```

---

## `cmd/cli/cmd/client.go` — HTTP Client Helper

```go
var (
    apiBase             = "http://localhost"
    httpClientOverride  *http.Client  // set in tests
)

func apiClient() *http.Client {
    if httpClientOverride != nil { return httpClientOverride }
    return ipc.NewHTTPClient()
}

func apiURL(path string) string {
    return apiBase + path
}
```

For `cmd/standalone/`, `cmd/manager/`, `cmd/worker/` — each has its own client.go
that uses regular `http.DefaultClient` or a custom client with TCP dial (no IPC).

---

## Key Patterns to Follow

### 1. Cobra command structure
```go
var serveCmd = &cobra.Command{
    Use:   "serve",
    Short: "...",
    RunE:  runServe,  // use RunE not Run to propagate errors
}

func init() {
    rootCmd.AddCommand(serveCmd)
    serveCmd.Flags().Int("port", 8080, "HTTP port")
}
```

### 2. Viper binding
```go
// In init():
viper.BindPFlag("port", serveCmd.Flags().Lookup("port"))
viper.SetDefault("port", 8080)
```

### 3. Signal handling
```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
```

### 4. Error handling in handlers
```go
func writeJSON(w http.ResponseWriter, code int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, code int, msg string) {
    writeJSON(w, code, ErrorResponse{Error: msg})
}
```

### 5. Squirrel usage (MySQL vs Postgres)
```go
// mysql_jobs.go
var sqb = sq.StatementBuilder.PlaceholderFormat(sq.Question)

// postgres_jobs.go
var sqb = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Both use same query building API:
query, args, err := sqb.Select("id", "magnet", "status").
    From("jobs").
    Where(sq.Eq{"status": "queued"}).
    OrderBy("created_at ASC").
    Limit(1).
    ToSql()
```
