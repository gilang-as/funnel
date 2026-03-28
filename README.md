# Funnel

[![CI](https://github.com/gilang-as/funnel/actions/workflows/ci.yml/badge.svg)](https://github.com/gilang-as/funnel/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/gilang-as/funnel)](https://github.com/gilang-as/funnel/releases/latest)
[![Coverage](https://codecov.io/gh/gilang-as/funnel/branch/main/graph/badge.svg)](https://codecov.io/gh/gilang-as/funnel)
[![Go Reference](https://pkg.go.dev/badge/gopkg.gilang.dev/funnel.svg)](https://pkg.go.dev/gopkg.gilang.dev/funnel)
[![Go](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go&logoColor=white)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**Funnel** downloads BitTorrent content and streams it directly to S3-compatible object storage via a chunk-based multipart upload pipeline. Files are **never permanently stored on disk** — only a rolling buffer of at most 2 chunks (×10 MB) exists locally at any time.

---

## Features

- **Zero disk I/O** — torrent pieces flow directly into S3 multipart uploads; at most 20 MB lives on disk at once
- **Resumable** — multipart upload state and piece completion markers are persisted to S3
- **Seeding from S3** — after download completes, Funnel seeds directly from S3
- **S3-compatible** — works with AWS S3, MinIO, Backblaze B2, Cloudflare R2, and any S3 API endpoint
- **Local fallback** — can write to a local directory instead of S3
- **Queue management** — configurable concurrent download limit (default 3); excess torrents wait and start automatically
- **Full lifecycle** — add, pause, resume, stop, remove via CLI or REST API
- **Three deployment modes** — personal CLI daemon, standalone TCP server, distributed cluster
- **Cross-platform** — macOS, Linux, Windows (static binaries, no CGO)
- **Autostart** — systemd (Linux), LaunchAgents (macOS), registry (Windows)

---

## How It Works

```mermaid
flowchart TD
    A([magnet link])
    B[BitTorrent peers]
    C["chunk buffer (≤20 MB)"]
    D[S3 multipart upload part]
    E[chunk deleted from disk]
    F[CompleteMultipartUpload]
    G[seed from S3]

    A --> B
    B -->|piece data| C
    C -->|chunk complete| D
    D --> E
    E -->|all chunks done| F
    F --> G
    G -->|peers| B
```

---

## Deployment Modes

| Mode | Binary | Best For |
|------|--------|----------|
| **CLI daemon** | `funnel` | Personal use on a workstation; communicates over Unix socket |
| **Standalone** | `funneld` | Single-server deployment; exposes REST API over TCP |
| **Cluster** | `funnel-manager` + `funnel-worker` | High-availability multi-node setup |

---

## Installation

### Install Script (macOS / Linux)

Detects your OS and architecture automatically, verifies the SHA-256 checksum, and installs to `/usr/local/bin`.

```bash
curl -fsSL https://raw.githubusercontent.com/gilang-as/funnel/main/install.sh | bash
```

Options via environment variables:

```bash
# Install funneld (standalone daemon) instead of funnel
BINARY=funneld bash <(curl -fsSL https://raw.githubusercontent.com/gilang-as/funnel/main/install.sh)

# Pin a specific version
VERSION=v1.2.3 bash <(curl -fsSL https://raw.githubusercontent.com/gilang-as/funnel/main/install.sh)

# Custom install directory (no sudo needed)
INSTALL_DIR=~/.local/bin bash <(curl -fsSL https://raw.githubusercontent.com/gilang-as/funnel/main/install.sh)
```

Supported platforms: macOS amd64/arm64, Linux amd64/arm64.

### Download Pre-built Binary

Download from [GitHub Releases](https://github.com/gilang-as/funnel/releases/latest).

Archives follow the naming convention `<binary>_<version>_<os>_<arch>.<ext>`:

| Platform | File |
|----------|------|
| macOS Apple Silicon | `funnel_<version>_darwin_arm64.tar.gz` |
| macOS Intel | `funnel_<version>_darwin_amd64.tar.gz` |
| Linux x86\_64 | `funnel_<version>_linux_amd64.tar.gz` |
| Linux ARM64 | `funnel_<version>_linux_arm64.tar.gz` |
| Windows x86\_64 | `funnel_<version>_windows_amd64.zip` |
| Linux x86\_64 (standalone) | `funneld_<version>_linux_amd64.tar.gz` |
| Linux ARM64 (standalone) | `funneld_<version>_linux_arm64.tar.gz` |

Each release also includes `.deb` and `.rpm` packages for Linux, and a `checksums.txt` with SHA-256 hashes for all artifacts.

```bash
# Debian / Ubuntu
sudo dpkg -i funnel_<version>_linux_amd64.deb

# RHEL / Fedora / CentOS
sudo rpm -i funnel_<version>_linux_amd64.rpm

# Windows (PowerShell)
Invoke-WebRequest https://github.com/gilang-as/funnel/releases/latest/download/funnel_<version>_windows_amd64.zip -OutFile funnel.zip
Expand-Archive funnel.zip
```

### go install

Requires Go 1.22+.

```bash
# CLI (personal daemon)
go install gopkg.gilang.dev/funnel/cmd/cli@latest

# Standalone daemon (TCP server)
go install gopkg.gilang.dev/funnel/cmd/standalone@latest

# Cluster manager
go install gopkg.gilang.dev/funnel/cmd/manager@latest

# Cluster worker
go install gopkg.gilang.dev/funnel/cmd/worker@latest
```

### Homebrew (macOS / Linux)

```bash
brew install gilang-as/tap/funnel
```

### APT (Debian / Ubuntu)

```bash
curl -fsSL https://pkg.gilang.dev/gpg | sudo gpg --dearmor -o /usr/share/keyrings/funnel.gpg
echo "deb [signed-by=/usr/share/keyrings/funnel.gpg] https://pkg.gilang.dev/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/funnel.list
sudo apt update && sudo apt install funnel
```

### APK (Alpine Linux)

```bash
echo "https://pkg.gilang.dev/apk" >> /etc/apk/repositories
apk add --no-cache funnel
```

### RPM (RHEL / Fedora / CentOS)

```bash
dnf config-manager --add-repo https://pkg.gilang.dev/rpm/funnel.repo
dnf install funnel
```

---

## Quick Start

### Mode 1: Personal CLI Daemon

The `funnel` CLI controls a local daemon over a Unix socket (no TCP port opened).

```bash
# 1. Start daemon in background
funnel start

# 2. Configure storage (optional — defaults to ~/Downloads/funnel)
cat > ~/.config/funnel/config.yaml << 'EOF'
storage:
  type: s3
  s3:
    endpoint: https://s3.amazonaws.com
    bucket: my-bucket
    access-key: AKIAIOSFODNN7EXAMPLE
    secret-key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
    region: us-east-1
EOF

# 3. Add a torrent
funnel add "magnet:?xt=urn:btih:..."

# 4. Monitor progress
funnel list
funnel status
```

### Mode 2: Standalone Server (funneld)

`funneld` exposes the REST API over a TCP port. Suitable for a home server or VPS.

```bash
funneld serve \
  --port 8080 \
  --storage.s3.endpoint http://minio:9000 \
  --storage.s3.bucket funnel \
  --storage.s3.access-key user \
  --storage.s3.secret-key password
```

Use the API directly:
```bash
curl -s http://localhost:8080/api/status
curl -s -X POST http://localhost:8080/api/torrents \
  -H "Content-Type: application/json" \
  -d '{"magnet":"magnet:?xt=urn:btih:..."}'
```

### Mode 3: Cluster (Manager + Workers)

```bash
# 1. Start the manager (requires MySQL or Postgres)
funnel-manager serve \
  --port 8080 \
  --db-driver postgres \
  --db-dsn "postgres://user:pass@localhost:5432/funnel?sslmode=disable"

# 2. Create a join token
funnel-manager token create --name "worker-1"
# → Token: fnl_xxxxxxxxxxxxxxxx

# 3. Start one or more workers
funnel-worker run \
  --manager http://manager:8080 \
  --token fnl_xxxxxxxxxxxxxxxx \
  --capacity 5 \
  --storage-type s3 \
  --s3-bucket funnel \
  --s3-endpoint http://minio:9000 \
  --s3-access-key user \
  --s3-secret-key password
```

---

## Configuration

Config file location: `~/.config/funnel/config.yaml`

All keys can also be set via environment variables with the `FUNNEL_` prefix (dots and hyphens become underscores, e.g. `FUNNEL_STORAGE_S3_BUCKET`).

### Full Config Reference

```yaml
# Storage backend
storage:
  type: local          # "local" or "s3"
  local:
    dir: ~/Downloads/funnel
  s3:
    endpoint: https://s3.amazonaws.com   # or MinIO URL
    bucket: my-bucket
    access-key: AKIAIOSFODNN7EXAMPLE
    secret-key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
    region: us-east-1
    base-dir: downloads    # prefix/folder inside the bucket

# Download behaviour
upload-rate: 524288    # bytes/sec upload cap (0 = unlimited, default 512 KB/s)
max-active: 3          # max concurrent downloads

# Standalone / cluster options
port: 8080             # TCP port (funneld / funnel-manager)
state: file            # funneld state store: "file", "mysql", "postgres"
db-dsn: ""             # DSN for mysql/postgres state store
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `FUNNEL_STORAGE_TYPE` | `local` | `local` or `s3` |
| `FUNNEL_STORAGE_LOCAL_DIR` | `~/Downloads/funnel` | Local storage directory |
| `FUNNEL_STORAGE_S3_ENDPOINT` | — | S3 endpoint URL |
| `FUNNEL_STORAGE_S3_BUCKET` | — | S3 bucket name |
| `FUNNEL_STORAGE_S3_ACCESS_KEY` | — | S3 access key |
| `FUNNEL_STORAGE_S3_SECRET_KEY` | — | S3 secret key |
| `FUNNEL_STORAGE_S3_REGION` | `us-east-1` | S3 region |
| `FUNNEL_STORAGE_S3_BASE_DIR` | `downloads` | Key prefix inside the bucket |
| `FUNNEL_UPLOAD_RATE` | `524288` | Upload rate cap in bytes/sec |
| `FUNNEL_MAX_ACTIVE` | `3` | Max concurrent downloads |
| `FUNNEL_JOIN_TOKEN` | — | Worker cluster join token |

---

## Docker

Images are published to [Docker Hub](https://hub.docker.com/r/gilangas/funnel) (`gilangas/funnel`) and support **linux/amd64** and **linux/arm64**.

### Available Images

| Image | Description |
|-------|-------------|
| `gilangas/funnel:standalone` | `funneld` — single-node TCP daemon |
| `gilangas/funnel:manager` | `funnel-manager` — cluster coordinator |
| `gilangas/funnel:worker` | `funnel-worker` — cluster worker node |

### Tag Strategy

Each service follows the same tagging pattern:

| Tag | Meaning |
|-----|---------|
| `gilangas/funnel:<cmd>` | Latest stable (e.g. `standalone`) |
| `gilangas/funnel:<cmd>-latest` | Alias for latest stable |
| `gilangas/funnel:<cmd>-v1.2.3` | Pinned version |

```bash
# Always latest
docker pull gilangas/funnel:standalone

# Pinned version (recommended for production)
docker pull gilangas/funnel:standalone-v1.2.3

# Manager and worker
docker pull gilangas/funnel:manager
docker pull gilangas/funnel:worker
```

All images are built from `scratch` (binary + CA certificates only) and include SBOM and provenance attestation.

### Run Standalone

```bash
# With S3 / MinIO
docker run -d \
  --name funneld \
  -p 8080:8080 \
  -e FUNNEL_STORAGE_TYPE=s3 \
  -e FUNNEL_STORAGE_S3_ENDPOINT=http://minio:9000 \
  -e FUNNEL_STORAGE_S3_BUCKET=funnel \
  -e FUNNEL_STORAGE_S3_ACCESS_KEY=user \
  -e FUNNEL_STORAGE_S3_SECRET_KEY=password \
  gilangas/funnel:standalone serve --port 8080

# With local storage (mount a volume)
docker run -d \
  --name funneld \
  -p 8080:8080 \
  -v funnel-data:/downloads \
  -e FUNNEL_STORAGE_TYPE=local \
  -e FUNNEL_STORAGE_LOCAL_DIR=/downloads \
  gilangas/funnel:standalone serve --port 8080
```

### Run Cluster

```bash
# 1. Start manager (requires Postgres or MySQL)
docker run -d \
  --name funnel-manager \
  -p 8080:8080 \
  -e FUNNEL_DB_DRIVER=postgres \
  -e FUNNEL_DB_DSN=postgres://funnel:secret@postgres:5432/funnel?sslmode=disable \
  gilangas/funnel:manager serve --port 8080

# 2. Create a join token
docker exec funnel-manager funnel-manager token create --name worker-1
# → Token: fnl_xxxxxxxxxxxxxxxx

# 3. Start a worker
docker run -d \
  --name funnel-worker \
  -e FUNNEL_MANAGER=http://funnel-manager:8080 \
  -e FUNNEL_JOIN_TOKEN=fnl_xxxxxxxxxxxxxxxx \
  -e FUNNEL_CAPACITY=5 \
  -e FUNNEL_STORAGE_TYPE=s3 \
  -e FUNNEL_STORAGE_S3_ENDPOINT=http://minio:9000 \
  -e FUNNEL_STORAGE_S3_BUCKET=funnel \
  -e FUNNEL_STORAGE_S3_ACCESS_KEY=user \
  -e FUNNEL_STORAGE_S3_SECRET_KEY=password \
  gilangas/funnel:worker run
```

---

## Docker Compose

### Development (Standalone + MinIO)

```yaml
services:
  funneld:
    image: gilangas/funnel:standalone
    command: serve --port 8080
    ports:
      - "8080:8080"
    environment:
      FUNNEL_STORAGE_TYPE: s3
      FUNNEL_STORAGE_S3_ENDPOINT: http://minio:9000
      FUNNEL_STORAGE_S3_BUCKET: funnel
      FUNNEL_STORAGE_S3_ACCESS_KEY: user
      FUNNEL_STORAGE_S3_SECRET_KEY: password
    depends_on:
      minio:
        condition: service_healthy

  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: user
      MINIO_ROOT_PASSWORD: password
    volumes:
      - minio-data:/data
    ports:
      - "9000:9000"
      - "9001:9001"   # Web console: http://localhost:9001
    healthcheck:
      test: ["CMD", "mc", "ready", "local"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  minio-data:
```

### Production Cluster (Manager + Workers + MinIO)

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: funnel
      POSTGRES_USER: funnel
      POSTGRES_PASSWORD: secret
    volumes:
      - pg-data:/var/lib/postgresql/data

  manager:
    image: gilangas/funnel:manager
    command: serve --port 8080
    ports:
      - "8080:8080"
    environment:
      FUNNEL_DB_DRIVER: postgres
      FUNNEL_DB_DSN: postgres://funnel:secret@postgres:5432/funnel?sslmode=disable
    depends_on:
      - postgres

  worker:
    image: gilangas/funnel:worker
    command: run
    environment:
      FUNNEL_MANAGER: http://manager:8080
      FUNNEL_JOIN_TOKEN: ${FUNNEL_JOIN_TOKEN}
      FUNNEL_CAPACITY: 5
      FUNNEL_STORAGE_TYPE: s3
      FUNNEL_STORAGE_S3_ENDPOINT: http://minio:9000
      FUNNEL_STORAGE_S3_BUCKET: funnel
      FUNNEL_STORAGE_S3_ACCESS_KEY: user
      FUNNEL_STORAGE_S3_SECRET_KEY: password
    depends_on:
      - manager

  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: user
      MINIO_ROOT_PASSWORD: password
    volumes:
      - minio-data:/data
    ports:
      - "9000:9000"
      - "9001:9001"

volumes:
  pg-data:
  minio-data:
```

Scale workers: `docker compose up -d --scale worker=5`

---

## Kubernetes

### Standalone

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: funneld
spec:
  replicas: 1
  selector:
    matchLabels:
      app: funneld
  template:
    metadata:
      labels:
        app: funneld
    spec:
      containers:
        - name: funneld
          image: gilangas/funnel:standalone
          args: ["serve", "--port", "8080"]
          ports:
            - containerPort: 8080
          env:
            - name: FUNNEL_STORAGE_TYPE
              value: s3
            - name: FUNNEL_STORAGE_S3_ENDPOINT
              value: http://minio.default.svc.cluster.local:9000
            - name: FUNNEL_STORAGE_S3_BUCKET
              value: funnel
            - name: FUNNEL_STORAGE_S3_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: funnel-s3-creds
                  key: access-key
            - name: FUNNEL_STORAGE_S3_SECRET_KEY
              valueFrom:
                secretKeyRef:
                  name: funnel-s3-creds
                  key: secret-key
---
apiVersion: v1
kind: Service
metadata:
  name: funneld
spec:
  selector:
    app: funneld
  ports:
    - port: 8080
      targetPort: 8080
```

### Cluster (Manager + Workers)

```yaml
# Manager Deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: funnel-manager
spec:
  replicas: 1
  selector:
    matchLabels:
      app: funnel-manager
  template:
    metadata:
      labels:
        app: funnel-manager
    spec:
      containers:
        - name: manager
          image: gilangas/funnel:manager
          args: ["serve", "--port", "8080"]
          ports:
            - containerPort: 8080
          env:
            - name: FUNNEL_DB_DRIVER
              value: postgres
            - name: FUNNEL_DB_DSN
              valueFrom:
                secretKeyRef:
                  name: funnel-db
                  key: dsn
---
apiVersion: v1
kind: Service
metadata:
  name: funnel-manager
spec:
  selector:
    app: funnel-manager
  ports:
    - port: 8080
      targetPort: 8080
---
# Worker Deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: funnel-worker
spec:
  replicas: 3   # scale as needed
  selector:
    matchLabels:
      app: funnel-worker
  template:
    metadata:
      labels:
        app: funnel-worker
    spec:
      containers:
        - name: worker
          image: gilangas/funnel:worker
          args: ["run"]
          env:
            - name: FUNNEL_MANAGER
              value: http://funnel-manager:8080
            - name: FUNNEL_JOIN_TOKEN
              valueFrom:
                secretKeyRef:
                  name: funnel-join-token
                  key: token
            - name: FUNNEL_CAPACITY
              value: "5"
            - name: FUNNEL_STORAGE_TYPE
              value: s3
            - name: FUNNEL_STORAGE_S3_ENDPOINT
              value: http://minio.default.svc.cluster.local:9000
            - name: FUNNEL_STORAGE_S3_BUCKET
              value: funnel
            - name: FUNNEL_STORAGE_S3_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: funnel-s3-creds
                  key: access-key
            - name: FUNNEL_STORAGE_S3_SECRET_KEY
              valueFrom:
                secretKeyRef:
                  name: funnel-s3-creds
                  key: secret-key
```

Generate a join token and create the secret:
```bash
kubectl exec -it deploy/funnel-manager -- \
  funnel-manager token create --name "k8s-workers"
# → Token: fnl_xxxxxxxxxxxxxxxx

kubectl create secret generic funnel-join-token \
  --from-literal=token=fnl_xxxxxxxxxxxxxxxx
```

---

## CLI Reference

### Daemon Management

| Command | Description |
|---------|-------------|
| `funnel start` | Start the daemon in the background |
| `funnel daemon` | Run daemon in the foreground (for use by init systems) |
| `funnel shutdown` | Gracefully stop the running daemon |
| `funnel status` | Show daemon status and per-state torrent counts |

### Torrent Lifecycle

| Command | Description |
|---------|-------------|
| `funnel add <magnet>` | Add a torrent (queued immediately) |
| `funnel list` | List all torrents |
| `funnel list -d` | Downloading only |
| `funnel list -s` | Seeding only |
| `funnel list -p` | Paused only |
| `funnel list -q` | Queued only |
| `funnel list -f` | Failed only |
| `funnel pause <id>` | Pause a torrent |
| `funnel resume <id>` | Resume a paused torrent |
| `funnel stop <id>` | Disconnect torrent from client, remove from list (data kept) |
| `funnel remove <id>` | Remove torrent and delete all data from storage |

### Autostart

```bash
funnel autostart enable    # register with OS init system
funnel autostart disable   # unregister
```

Platform support:
- **macOS** — LaunchAgent plist (`~/Library/LaunchAgents/dev.gilang.funnel.plist`)
- **Linux** — systemd user unit (`~/.config/systemd/user/funnel.service`)
- **Windows** — registry key (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`)

### Global Flags

```
--config string   config file path (default: ~/.config/funnel/config.yaml)
--socket string   override IPC socket path
```

### Cluster Token Management (funnel-manager)

| Command | Description |
|---------|-------------|
| `funnel-manager token create --name <name>` | Create a worker join token |
| `funnel-manager token list` | List all tokens |
| `funnel-manager token revoke <id>` | Revoke a token |

---

## Torrent States

```mermaid
stateDiagram-v2
    [*] --> queued : add

    queued --> downloading : slot available
    queued --> [*] : stop (data kept)

    downloading --> seeding : complete
    downloading --> paused : pause
    downloading --> failed : error

    seeding --> paused : pause
    seeding --> [*] : stop / remove

    paused --> queued : resume
    paused --> [*] : stop / remove
```

| State | Meaning |
|-------|---------|
| `queued` | Waiting for a download slot |
| `downloading` | Actively downloading |
| `seeding` | Upload complete, seeding from storage |
| `paused` | Inactive; stays in list and is resumable |
| `failed` | Error during download |

---

## REST API

All endpoints are available on:
- **CLI daemon**: `http://localhost` over Unix socket/Named Pipe (prefer the `funnel` CLI)
- **funneld / manager**: `http://host:port`

| Method | Path | Body / Params | Description |
|--------|------|---------------|-------------|
| `POST` | `/api/torrents` | `{"magnet":"..."}` | Add a torrent |
| `GET` | `/api/torrents` | `?status=downloading` | List torrents (optional filter) |
| `PATCH` | `/api/torrents/{id}` | `{"action":"pause"\|"resume"}` | Pause or resume |
| `POST` | `/api/torrents/{id}/stop` | — | Stop (data kept) |
| `DELETE` | `/api/torrents/{id}` | — | Remove + delete data |
| `GET` | `/api/status` | — | Daemon info and per-state counts |
| `POST` | `/api/shutdown` | — | Graceful shutdown |

### Examples

```bash
# Add torrent
curl -X POST http://localhost:8080/api/torrents \
  -H "Content-Type: application/json" \
  -d '{"magnet":"magnet:?xt=urn:btih:..."}'
# → {"id":"abc123","status":"queued","new":true}

# List all
curl http://localhost:8080/api/torrents

# Filter by status
curl "http://localhost:8080/api/torrents?status=downloading"

# Pause
curl -X PATCH http://localhost:8080/api/torrents/abc123 \
  -H "Content-Type: application/json" \
  -d '{"action":"pause"}'

# Daemon status
curl http://localhost:8080/api/status
# → {"running":true,"counts":{"queued":1,"downloading":2,"seeding":3},"storage":{"type":"s3","location":"..."}}
```

---

## S3 Object Layout

```
{infoHash}/files/{name}                        # completed file
{infoHash}/files/{path/to/name}                # multi-file torrent
{infoHash}/state/multipart/{fileHex}.json      # in-progress multipart upload state
{infoHash}/state/{pieceIndex}                  # piece completion marker
{infoHash}/metainfo.json                       # torrent metainfo cache
```

---

## IPC Transport (CLI Daemon)

The `funnel` CLI daemon uses a Unix domain socket (macOS/Linux) or Named Pipe (Windows) for IPC — no TCP port is opened.

Default socket paths:
- **macOS**: `~/Library/Application Support/funnel/funnel.sock`
- **Linux**: `$XDG_RUNTIME_DIR/funnel.sock` → fallback `~/.local/share/funnel/funnel.sock`
- **Windows**: `\\.\pipe\funnel`

Override with `--socket /path/to/custom.sock`.

---

## Development

### Prerequisites

- Go 1.22+
- Docker (for MinIO in S3 integration tests)

### Setup

```bash
git clone https://github.com/gilang-as/funnel.git
cd funnel

go mod download

# Start MinIO for S3 tests
docker compose up -d
```

### Build

```bash
make build           # build CLI binary → ./funnel
make build-race      # build with race detector
make install         # build + install to /usr/local/bin/funnel

# Cross-compile manually
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o funneld-linux-arm64 ./cmd/standalone
```

### Test

```bash
# All tests
go test ./...

# With race detector (recommended)
go test -race ./...

# S3 integration tests (requires MinIO)
make test-s3

# Specific package
go test -race ./internal/daemon/

# With coverage
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Test Packages

| Package | Test File | What it covers |
|---------|-----------|----------------|
| `funnel` (root) | `storage_test.go` | S3 storage engine, multipart upload, piece completion, GC |
| `internal/daemon` | `state_test.go` | State persistence (add, remove, update, reload) |
| `internal/daemon` | `server_test.go` | All HTTP handlers via mock manager |
| `internal/ipc` | `ipc_test.go` | SocketPath, SetSocketPath, platform detection |
| `cmd/cli/cmd` | `commands_test.go` | CLI commands against httptest server |

### Vet & Lint

```bash
go vet ./...

# Optional
golangci-lint run
```

### Local MinIO Credentials

| | |
|--|--|
| Endpoint | `http://localhost:9000` |
| Console | `http://localhost:9001` |
| Access Key | `user` |
| Secret Key | `password` |

---

## Project Structure

```
funnel/                         # root package — S3 storage engine
  s3.go                         # S3Client interface, Config, S3Storage, NewS3Storage
  torrent.go                    # s3Torrent, s3Piece, read/write dispatch logic
  file.go                       # s3FileState, s3Chunk, multipart upload, GC
  piece.go                      # s3PieceCompletion, S3 piece markers
  storage_test.go               # integration-style tests

storages/
  s3.go                         # S3Config + NewS3Storage factory
  local.go                      # localStorageImpl wrapping anacrolix file storage

internal/daemon/
  types.go                      # TorrentInfo, Status, request/response types
  state.go                      # SavedTorrent, State — JSON persistence
  statestore.go                 # StateStore interface (file / MySQL / Postgres / memory)
  manager.go                    # Manager: torrent lifecycle, queue logic
  server.go                     # HTTP handler, managerIface, all routes

internal/ipc/
  ipc.go                        # SocketPath(), SetSocketPath()
  listener_unix.go / _windows   # IPC listener (Unix socket / Named Pipe)
  dialer_unix.go / _windows     # HTTP client over IPC

internal/cluster/               # Cluster coordinator logic
internal/store/                 # Database store (MySQL, Postgres, memory)

cmd/cli/                        # funnel       — personal CLI daemon
cmd/standalone/                 # funneld      — standalone TCP daemon
cmd/manager/                    # funnel-manager — cluster coordinator
cmd/worker/                     # funnel-worker  — cluster worker node

Dockerfile                      # Multi-binary image (ARG CMD=standalone|manager|worker)
docker-compose.yml              # MinIO local dev environment
```

---

## Reporting Bugs

Found a bug? Please open an issue on GitHub:

**[https://github.com/gilang-as/funnel/issues/new](https://github.com/gilang-as/funnel/issues/new)**

Include:
- Funnel version (`funnel --version`)
- OS and architecture
- Deployment mode (CLI / standalone / cluster)
- Storage type (local / S3 provider name)
- Steps to reproduce
- Relevant logs or error output

---

## Contributing

Pull requests are welcome! For significant changes, open an issue first to discuss the approach.

1. Fork the repo and create a feature branch:
   ```bash
   git checkout -b feat/my-feature
   ```
2. Make your changes with tests
3. Run tests and vet:
   ```bash
   go test -race ./...
   go vet ./...
   ```
4. Commit using [Conventional Commits](https://www.conventionalcommits.org/):
   ```
   feat: add auto-retry for failed multipart parts
   fix: prevent race in watchTorrent goroutine
   docs: add Kubernetes deployment examples
   ```
5. Open a Pull Request against `main`

### Code Style

- Standard `gofmt` formatting (enforced by CI)
- No CGO — all code must compile with `CGO_ENABLED=0`
- Lock ordering: always acquire `m.mu` (Manager) before `mt.mu` (managedTorrent)
- Context propagation: pass `ctx` through all I/O calls; use `s3ReadCtx()` / `s3WriteCtx()` helpers
- Tests: use table-driven tests; `httptest` for HTTP handler tests; inject `mockManager` via `managerIface`

---

## License

MIT — see [LICENSE](LICENSE).
