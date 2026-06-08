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

# Remove existing binary first — on Linux, writing to a binary held open by a
# running process (e.g. the daemon) fails with ETXTBSY / curl error 23.
# Unlinking first is safe: the kernel keeps the inode alive until the process exits.
rm -f "${TARGET}"

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

# Restart the Linux systemd user service if it is already running, so the new
# binary takes effect without a manual restart.  Best-effort: a restart failure
# must not abort the installer under set -e (service may fail with the new
# binary, transient unit issues, or container environments without systemd).
if command -v systemctl >/dev/null 2>&1; then
  if systemctl --user is-active model-shelf >/dev/null 2>&1; then
    if systemctl --user restart model-shelf >/dev/null 2>&1; then
      echo "Daemon service restarted."
    else
      echo "warning: failed to restart model-shelf service — run 'systemctl --user restart model-shelf' manually" >&2
    fi
  fi
fi

# Ensure PATH includes INSTALL_DIR for non-interactive sessions (e.g. SSH).
# We append to ~/.profile which is sourced by login shells (including non-interactive SSH).
PATH_LINE="export PATH=\"${INSTALL_DIR}:\$PATH\""
PROFILE="$HOME/.profile"

add_to_profile() {
  if [ -f "$PROFILE" ] && grep -qF "$INSTALL_DIR" "$PROFILE" 2>/dev/null; then
    # Entry already exists — nothing to do.
    return 1
  fi
  echo "" >> "$PROFILE"
  echo "# Added by model-shelf installer" >> "$PROFILE"
  echo "$PATH_LINE" >> "$PROFILE"
  return 0
}

if add_to_profile; then
  echo "Updated ${PROFILE} with PATH entry."
  case ":$PATH:" in
    *":${INSTALL_DIR}:"*)
      # Already on PATH in this session — no activation instructions needed.
      ;;
    *)
      echo ""
      echo "ℹ ${INSTALL_DIR} has been added to ${PROFILE}."
      echo "  Run 'source ${PROFILE}' or start a new login shell to activate."
      echo ""
      ;;
  esac
fi
