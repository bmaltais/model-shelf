#!/bin/sh
# install.sh — auto-detect OS/arch and install model-shelf to ~/.local/bin
set -e

REPO="bmaltais/model-shelf"
INSTALL_DIR="${MODEL_SHELF_INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Linux*)  OS="linux" ;;
  Darwin*) OS="darwin" ;;
  MINGW*|MSYS*|CYGWIN*) OS="windows" ;;
  *)
    echo "error: unsupported OS: $OS" >&2
    exit 1
    ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# Build download URL and target path
BINARY="model-shelf-${OS}-${ARCH}"
TARGET="${INSTALL_DIR}/model-shelf"
if [ "$OS" = "windows" ]; then
  BINARY="${BINARY}.exe"
  TARGET="${TARGET}.exe"
fi
URL="https://github.com/${REPO}/releases/latest/download/${BINARY}"

echo "model-shelf installer"
echo "  OS:      $OS"
echo "  Arch:    $ARCH"
echo "  Target:  ${TARGET}"
echo ""

# Create install directory
mkdir -p "$INSTALL_DIR"

# Download
echo "Downloading ${URL}..."
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$URL" -o "${TARGET}"
elif command -v wget >/dev/null 2>&1; then
  wget -q "$URL" -O "${TARGET}"
else
  echo "error: curl or wget is required" >&2
  exit 1
fi

# Make executable
chmod +x "${TARGET}"

# Verify
VERSION=$("${TARGET}" version 2>/dev/null) && echo "Installed: $VERSION" \
  || echo "warning: installed binary but could not verify (may need different platform)" >&2

# Check PATH
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    echo "⚠ ${INSTALL_DIR} is not on your PATH."
    echo "  Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
    echo ""
    echo "    export PATH=\"${INSTALL_DIR}:\$PATH\""
    echo ""
    ;;
esac
