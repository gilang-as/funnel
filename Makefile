BINARY   := funnel
CMD      := ./cmd/cli
INSTALL  := /usr/local/bin/$(BINARY)
VERSION  := Development
PKG      := gopkg.gilang.dev/funnel/cmd/cli/cmd
LDFLAGS  := -ldflags "-s -w -X $(PKG).Version=$(VERSION)"
CGO_ENABLED := 0

.PHONY: build test vet lint clean install uninstall \
        daemon start stop status list add minio minio-down

# ── Build ─────────────────────────────────────────────────────────────────────

build:
	CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BINARY) $(CMD)

build-race:
	go build -race $(LDFLAGS) -o $(BINARY) $(CMD)

# ── Test ──────────────────────────────────────────────────────────────────────

test:
	go test ./...

test-race:
	go test -race ./...

test-unit:
	go test ./internal/... ./cmd/...

test-s3:
	go test -race -run TestS3 -v ./...

vet:
	go vet ./...

# ── Install / Uninstall ───────────────────────────────────────────────────────

install: build
	cp $(BINARY) $(INSTALL)
	@echo "installed → $(INSTALL)"

uninstall:
	rm -f $(INSTALL)
	@echo "removed $(INSTALL)"

# ── Daemon lifecycle (requires installed or built binary) ─────────────────────

daemon: build
	./$(BINARY) daemon

start: build
	./$(BINARY) start

stop:
	./$(BINARY) shutdown

status:
	./$(BINARY) status

# ── Torrent management ────────────────────────────────────────────────────────
# Usage: make add MAGNET='magnet:?xt=urn:btih:...'

add:
	@test -n "$(MAGNET)" || (echo "Usage: make add MAGNET='magnet:?xt=...'" && exit 1)
	./$(BINARY) add '$(MAGNET)'

list:
	./$(BINARY) list

# ── MinIO (local S3) ──────────────────────────────────────────────────────────

minio:
	docker compose up -d
	@echo "MinIO console → http://localhost:9001  (user/password)"

minio-down:
	docker compose down

# ── Clean ─────────────────────────────────────────────────────────────────────

clean:
	rm -f $(BINARY)
