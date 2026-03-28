# Build stage
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build static binary
RUN CGO_ENABLED=1 GOOS=linux go build -a -ldflags '-linkmode external -extldflags "-static"' -o funnel ./cmd/funnel/main.go

# Final stage
FROM scratch

COPY --from=builder /app/funnel /usr/local/bin/funnel
# Important: copy CA certificates for HTTPS to work
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

ENTRYPOINT ["/usr/local/bin/funnel"]
