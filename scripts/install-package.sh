#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
PACKAGE=""
MANIFEST=""
INSTALL_DIR="${CLIFF_INSTALL_DIR:-$HOME/.cliff}"
PORT="${PORT:-8080}"
START=0
FORCE=0
SKIP_CHECKSUM=0
EXPECTED_ARCHIVE_SHA256=""

require_arg() {
  option="$1"
  if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
    echo "Missing value for $option" >&2
    exit 1
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --package) require_arg "$1" "${2:-}"; PACKAGE="$2"; shift 2 ;;
    --manifest) require_arg "$1" "${2:-}"; MANIFEST="$2"; shift 2 ;;
    --install-dir) require_arg "$1" "${2:-}"; INSTALL_DIR="$2"; shift 2 ;;
    -p|--port) require_arg "$1" "${2:-}"; PORT="$2"; shift 2 ;;
    --start) START=1; shift ;;
    --force) FORCE=1; shift ;;
    --skip-checksum) SKIP_CHECKSUM=1; shift ;;
    -h|--help)
      echo "Usage: sh scripts/install-package.sh [--package zip-or-url] [--manifest json-or-url] [--install-dir path] [-p 8080|--port 8080] [--start] [--force] [--skip-checksum]"
      exit 0
      ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

TEMP_ROOT="$(mktemp -d)"
cleanup() {
  rm -rf "$TEMP_ROOT"
}
trap cleanup EXIT

manifest_platform_field() {
  manifest_file="$1"
  platform="$2"
  field="$3"
  sed -n "/\"platform\"[[:space:]]*:[[:space:]]*\"$platform\"/,/^[[:space:]]*}[,]*[[:space:]]*$/p" "$manifest_file" |
    sed -n "s/.*\"$field\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" |
    head -n 1
}

if [ -n "$MANIFEST" ]; then
  # Detect the current platform.
  OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$OS" in
    linux*) OS="linux" ;;
    darwin*) OS="darwin" ;;
    *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
  esac
  ARCH="$(uname -m)"
  case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
  esac
  PLATFORM="$OS-$ARCH"

  case "$MANIFEST" in
    http://*|https://*)
      manifest_file="$TEMP_ROOT/cliff-release.json"
      if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$MANIFEST" -o "$manifest_file"
      elif command -v wget >/dev/null 2>&1; then
        wget -qO "$manifest_file" "$MANIFEST"
      else
        echo "curl or wget is required to download a release manifest URL." >&2
        exit 1
      fi
      base="${MANIFEST%/*}/"
      ;;
    *)
      manifest_file="$MANIFEST"
      base="$(cd "$(dirname "$manifest_file")" && pwd)/"
      ;;
  esac

  archive="$(manifest_platform_field "$manifest_file" "$PLATFORM" "archive")"
  EXPECTED_ARCHIVE_SHA256="$(manifest_platform_field "$manifest_file" "$PLATFORM" "sha256" | tr 'A-F' 'a-f')"

  if [ -z "$archive" ]; then
    echo "Release manifest does not include a package for platform '$PLATFORM'." >&2
    exit 1
  fi
  PACKAGE="$base$archive"
fi

if [ -z "$PACKAGE" ]; then
  if [ -f "$ROOT/dist/cliff-release.json" ]; then
    # Try the new platforms schema first, fall back to globbing for the platform zip.
    if [ -z "$PLATFORM" ]; then
      OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
      case "$OS" in linux*) OS="linux" ;; darwin*) OS="darwin" ;; esac
      ARCH="$(uname -m)"
      case "$ARCH" in x86_64|amd64) ARCH="amd64" ;; aarch64|arm64) ARCH="arm64" ;; esac
      PLATFORM="$OS-$ARCH"
    fi
    archive="$(manifest_platform_field "$ROOT/dist/cliff-release.json" "$PLATFORM" "archive")"
    if [ -n "$archive" ] && [ -f "$ROOT/dist/$archive" ]; then
      PACKAGE="$ROOT/dist/$archive"
    fi
  fi
fi

if [ -z "$PACKAGE" ]; then
  PACKAGE="$(ls -t "$ROOT"/dist/cliff-*.zip 2>/dev/null | head -n 1 || true)"
fi

if [ -z "$PACKAGE" ]; then
  echo "No Cliff package archive was found. Run npm run daemon:package or pass --package <zip-or-url>." >&2
  exit 1
fi

if ! command -v unzip >/dev/null 2>&1; then
  echo "unzip is required to install a Cliff package." >&2
  exit 1
fi

case "$PACKAGE" in
  http://*|https://*)
    if command -v curl >/dev/null 2>&1; then
      curl -fsSL "$PACKAGE" -o "$TEMP_ROOT/cliff.zip"
    elif command -v wget >/dev/null 2>&1; then
      wget -qO "$TEMP_ROOT/cliff.zip" "$PACKAGE"
    else
      echo "curl or wget is required to download a package URL." >&2
      exit 1
    fi
    PACKAGE="$TEMP_ROOT/cliff.zip"
    ;;
esac

verify_checksum() {
  archive_path="$1"
  checksum_path="$archive_path.sha256"

  if [ "$SKIP_CHECKSUM" = "1" ]; then
    echo "Warning: skipping package checksum verification." >&2
    return
  fi

  if [ ! -f "$checksum_path" ]; then
    if [ -n "$EXPECTED_ARCHIVE_SHA256" ]; then
      expected="$EXPECTED_ARCHIVE_SHA256"
    else
      echo "Warning: no checksum file found at $checksum_path; package integrity was not verified." >&2
      return
    fi
  else
    expected="$(awk '{print tolower($1)}' "$checksum_path")"
    if [ -n "$EXPECTED_ARCHIVE_SHA256" ] && [ "$expected" != "$EXPECTED_ARCHIVE_SHA256" ]; then
      echo "Package checksum sidecar does not match release manifest archive hash." >&2
      exit 1
    fi
  fi

  case "$expected" in
    [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
    *) echo "Checksum file is invalid: $checksum_path" >&2; exit 1 ;;
  esac

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive_path" | awk '{print tolower($1)}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$archive_path" | awk '{print tolower($1)}')"
  else
    echo "Warning: sha256sum or shasum is required to verify package checksums." >&2
    return
  fi

  if [ "$actual" != "$expected" ]; then
    echo "Package checksum mismatch. Expected $expected but got $actual." >&2
    exit 1
  fi

  echo "Verified package SHA-256: $actual"
}

verify_checksum "$PACKAGE"

print_lan_urls() {
  found=0
  if command -v hostname >/dev/null 2>&1; then
    for address in $(hostname -I 2>/dev/null || true); do
      case "$address" in
        127.*|169.254.*|""|*:*|*.*.*.*.*) ;;
        *.*.*.*)
          echo "  http://$address:$PORT"
          found=1
          ;;
      esac
    done
  fi
}

# Create a symlink in PATH so `cliff` is available system-wide.
setup_path_symlink() {
  binary="$INSTALL_DIR/cliff"
  # Try the user-writable path first, then the system path.
  for target in "$HOME/.local/bin" /usr/local/bin; do
    if [ -d "$target" ] || mkdir -p "$target" 2>/dev/null; then
      if ln -sf "$binary" "$target/cliff" 2>/dev/null; then
        echo "Symlinked: $target/cliff -> $binary"
        if [ "$target" = "$HOME/.local/bin" ]; then
          # Ensure ~/.local/bin is in PATH for common shells.
          for rc in "$HOME/.bashrc" "$HOME/.zshrc"; do
            if [ -f "$rc" ] && ! grep -q '\.local/bin' "$rc" 2>/dev/null; then
              echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$rc"
              echo "Added ~/.local/bin to PATH in $(basename "$rc")"
            fi
          done
        fi
        return 0
      fi
    fi
  done
  echo "Could not create symlink. Use $INSTALL_DIR/cliff directly." >&2
  return 1
}

require_extracted_file() {
  relative="$1"
  if [ ! -e "$TEMP_ROOT/cliff/$relative" ]; then
    echo "Package archive is missing required file: cliff/$relative" >&2
    exit 1
  fi
}

unzip -q "$PACKAGE" -d "$TEMP_ROOT"
if [ ! -d "$TEMP_ROOT/cliff" ]; then
  echo "Package archive did not contain a cliff folder." >&2
  exit 1
fi
require_extracted_file "cliff"
require_extracted_file "web/index.html"
require_extracted_file "package-manifest.json"

if [ -x "$INSTALL_DIR/stop.sh" ]; then
  DATA_DIR=data FORCE=1 sh "$INSTALL_DIR/stop.sh" >/dev/null 2>&1 || true
fi

# Also stop a CLI-managed daemon if running.
if [ -x "$INSTALL_DIR/cliff" ]; then
  "$INSTALL_DIR/cliff" stop >/dev/null 2>&1 || true
fi

if [ -e "$INSTALL_DIR" ] && [ "$FORCE" != "1" ]; then
  if [ -x "$INSTALL_DIR/cliff" ]; then
    setup_path_symlink || true
    echo "Cliff is already installed at $INSTALL_DIR"
    echo "Run: cliff start"
    exit 0
  fi
  echo "Install directory already exists: $INSTALL_DIR, but $INSTALL_DIR/cliff was not found. Re-run with --force to replace it." >&2
  exit 1
fi

rm -rf "$INSTALL_DIR"
mkdir -p "$(dirname "$INSTALL_DIR")"
mv "$TEMP_ROOT/cliff" "$INSTALL_DIR"

# Make the binary executable.
chmod +x "$INSTALL_DIR/cliff" 2>/dev/null || true

setup_path_symlink || true

if [ "$START" = "1" ]; then
  "$INSTALL_DIR/cliff" start -p "$PORT"
  echo
  echo "Open a new terminal to use the 'cliff' command from PATH."
else
  echo "Cliff installed to $INSTALL_DIR"
  echo "Open a new terminal to use the 'cliff' command from PATH."
  echo "Then run: cliff start"
fi
