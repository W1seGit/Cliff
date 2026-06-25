#!/bin/bash
# install-playit-macos.sh
# Builds the playit.gg agent from source on macOS and installs the CLI binary
# into the Cliff-managed tools directory so the daemon can manage it like the
# prebuilt Windows/Linux binaries.
#
# This script is embedded into the Cliff daemon via go:embed and executed by
# the playit build manager. It prints step markers the daemon parses:
#
#   [cliff:step] <step-name>
#   [cliff:done]
#   [cliff:error] <message>
#
# It is idempotent: re-running skips work that is already complete.
set -euo pipefail

DEST_DIR="${1:?missing destination dir}"
SRC_DIR="${DEST_DIR}/src"
BINARY_PATH="${DEST_DIR}/playit"
PLAYIT_REPO="https://github.com/playit-cloud/playit-agent.git"

log_step() { printf '[cliff:step] %s\n' "$1"; }
log_done()  { printf '[cliff:done]\n'; }
log_error() { printf '[cliff:error] %s\n' "$1" >&2; }

trap 'log_error "build failed at step: ${current_step:-unknown}"; exit 1' ERR

# --- Step 1: prepare source checkout ---------------------------------------
current_step="cloning playit-agent"
log_step "$current_step"
mkdir -p "$SRC_DIR"
if [ -d "$SRC_DIR/.git" ]; then
  git -C "$SRC_DIR" fetch --quiet origin || true
  git -C "$SRC_DIR" reset --hard --quiet origin/$(git -C "$SRC_DIR" rev-parse --abbrev-ref HEAD) || true
else
  rm -rf "$SRC_DIR"
  git clone --depth 1 "$PLAYIT_REPO" "$SRC_DIR"
fi

# --- Step 2: build release binary -----------------------------------------
current_step="building (cargo build --release)"
log_step "$current_step"
# Ensure cargo is on PATH (rustup installs into ~/.cargo/bin).
if [ -f "$HOME/.cargo/env" ]; then
  # shellcheck disable=SC1091
  . "$HOME/.cargo/env"
fi
if ! command -v cargo >/dev/null 2>&1; then
  log_error "cargo not found on PATH after sourcing ~/.cargo/env"
  exit 1
fi
cd "$SRC_DIR"
cargo build --release

# --- Step 3: install the binary -------------------------------------------
current_step="installing binary"
log_step "$current_step"
BUILT_BIN="$SRC_DIR/target/release/playit"
if [ ! -f "$BUILT_BIN" ]; then
  log_error "expected built binary not found at $BUILT_BIN"
  exit 1
fi
mkdir -p "$DEST_DIR"
cp -f "$BUILT_BIN" "$BINARY_PATH"
chmod +x "$BINARY_PATH"

# --- Step 4: symlink into ~/.local/bin so `playit` is on PATH -------------
current_step="symlinking into PATH"
log_step "$current_step"
LOCAL_BIN="$HOME/.local/bin"
mkdir -p "$LOCAL_BIN"
ln -sf "$BINARY_PATH" "$LOCAL_BIN/playit"

log_done
