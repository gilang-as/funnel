#!/usr/bin/env bash
# Funnel installer — macOS and Linux (amd64 / arm64)
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/gilang-as/funnel/main/install.sh | bash
#
# Environment variables (all optional):
#   BINARY      — funnel (default) or funneld
#   VERSION     — e.g. v1.2.3  (default: latest)
#   INSTALL_DIR — installation directory (default: /usr/local/bin)
#
# Examples:
#   BINARY=funneld bash <(curl -fsSL ...)
#   VERSION=v1.2.3 bash <(curl -fsSL ...)
#   INSTALL_DIR=~/.local/bin bash <(curl -fsSL ...)

set -euo pipefail

REPO="gilang-as/funnel"
BINARY="${BINARY:-funnel}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# ── Detect OS ─────────────────────────────────────────────────────────────────

OS="$(uname -s)"
case "$OS" in
  Linux*)  OS="linux"  ;;
  Darwin*) OS="darwin" ;;
  *)
    echo "error: unsupported OS: $OS" >&2
    exit 1
    ;;
esac

# ── Detect architecture ───────────────────────────────────────────────────────

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# ── Validate binary / OS combination ─────────────────────────────────────────

if [ "$BINARY" != "funnel" ] && [ "$BINARY" != "funneld" ]; then
  echo "error: BINARY must be 'funnel' or 'funneld'" >&2
  exit 1
fi

if [ "$BINARY" = "funneld" ] && [ "$OS" != "linux" ]; then
  echo "error: funneld is only available for Linux" >&2
  exit 1
fi

# ── Resolve version ───────────────────────────────────────────────────────────

if [ -z "${VERSION:-}" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
fi

if [ -z "$VERSION" ]; then
  echo "error: could not determine latest version" >&2
  exit 1
fi

VER="${VERSION#v}"
ARCHIVE="${BINARY}_${VER}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

# ── Download ──────────────────────────────────────────────────────────────────

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Installing ${BINARY} ${VERSION} (${OS}/${ARCH})..."
echo ""

curl -fsSL --progress-bar "${BASE_URL}/${ARCHIVE}"      -o "${TMP}/${ARCHIVE}"
curl -fsSL                "${BASE_URL}/checksums.txt"   -o "${TMP}/checksums.txt"

# ── Verify checksum ───────────────────────────────────────────────────────────

echo "Verifying checksum..."

CHECKSUM_LINE=$(grep "${ARCHIVE}" "${TMP}/checksums.txt")

cd "$TMP"
if command -v sha256sum >/dev/null 2>&1; then
  echo "$CHECKSUM_LINE" | sha256sum -c -
elif command -v shasum >/dev/null 2>&1; then
  echo "$CHECKSUM_LINE" | shasum -a 256 -c -
else
  echo "warning: no checksum tool found, skipping verification" >&2
fi

# ── Extract ───────────────────────────────────────────────────────────────────

tar -xzf "${ARCHIVE}"
BINARY_SRC="${TMP}/${BINARY}_${VER}_${OS}_${ARCH}/${BINARY}"
chmod +x "$BINARY_SRC"

# ── Install ───────────────────────────────────────────────────────────────────

mkdir -p "$INSTALL_DIR"

if [ -w "$INSTALL_DIR" ]; then
  mv "$BINARY_SRC" "${INSTALL_DIR}/${BINARY}"
else
  echo "sudo required to write to ${INSTALL_DIR}"
  sudo mv "$BINARY_SRC" "${INSTALL_DIR}/${BINARY}"
fi

echo ""
echo "Installed ${INSTALL_DIR}/${BINARY}"
"${INSTALL_DIR}/${BINARY}" --version
