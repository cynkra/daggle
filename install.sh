#!/bin/sh
# Install daggle — a lightweight DAG scheduler for R
# Usage: curl -fsSL https://raw.githubusercontent.com/cynkra/daggle/main/install.sh | sh
set -e

REPO="cynkra/daggle"
INSTALL_DIR="${DAGGLE_INSTALL_DIR:-/usr/local/bin}"

# Detect OS and architecture
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# Get latest release tag
if command -v curl >/dev/null 2>&1; then
  LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed 's/.*"v\(.*\)".*/\1/')
elif command -v wget >/dev/null 2>&1; then
  LATEST=$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed 's/.*"v\(.*\)".*/\1/')
else
  echo "Error: curl or wget is required" >&2
  exit 1
fi

if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest version" >&2
  exit 1
fi

FILENAME="daggle_${LATEST}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/v${LATEST}/${FILENAME}"

echo "Installing daggle v${LATEST} (${OS}/${ARCH})..."

# Download and extract
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$URL" -o "$TMPDIR/$FILENAME"
else
  wget -q "$URL" -O "$TMPDIR/$FILENAME"
fi

tar -xzf "$TMPDIR/$FILENAME" -C "$TMPDIR"

# Install binary
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMPDIR/daggle" "$INSTALL_DIR/daggle"
else
  echo "Installing to $INSTALL_DIR (requires sudo)..."
  sudo mv "$TMPDIR/daggle" "$INSTALL_DIR/daggle"
fi

chmod +x "$INSTALL_DIR/daggle"

echo "daggle v${LATEST} installed to $INSTALL_DIR/daggle"
echo "Run 'daggle doctor' to verify your setup."
