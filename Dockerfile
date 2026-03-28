# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG CMD=standalone
ARG VERSION=development

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X gopkg.gilang.dev/funnel/cmd/${CMD}/cmd.Version=${VERSION}" \
    -o /app \
    ./cmd/${CMD}

# Final stage
FROM scratch
COPY --from=builder /app /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/app"]
