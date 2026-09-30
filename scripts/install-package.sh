#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
PACKAGE=""
MANIFEST=""
INSTALL_DIR="${CLIFF_INSTALL_DIR:-$HOME/.cliff}"
PORT="${PORT:-8080}"
DATA_DIR_OPT=""
SERVER_ROOT_OPT=""
START=0
FORCE=0
SKIP_CHECKSUM=0
EXPECTED_ARCHIVE_SHA256=""
PLATFORM=""

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
    --data-dir) require_arg "$1" "${2:-}"; DATA_DIR_OPT="$2"; shift 2 ;;
    --server-root) require_arg "$1" "${2:-}"; SERVER_ROOT_OPT="$2"; shift 2 ;;
    --start) START=1; shift ;;
    --force) FORCE=1; shift ;;
    --skip-checksum) SKIP_CHECKSUM=1; shift ;;
    -h|--help)
      echo "Usage: sh scripts/install-package.sh [--package zip-or-url] [--manifest json-or-url] [--install-dir path] [--data-dir path] [--server-root path] [-p 8080|--port 8080] [--start] [--skip-checksum]"
      echo "  --data-dir     Where Cliff keeps its settings and database (default: <install-dir>/data)"
      echo "  --server-root  Where your Minecraft servers live (default: <install-dir>/servers)"
      echo "  An existing folder is reused as it is; a missing one is created."
      echo "  Re-running over an existing Cliff install upgrades it and keeps data and servers."
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

# Stop a running Cliff before replacing its files.
if [ -x "$INSTALL_DIR/cliff" ]; then
  "$INSTALL_DIR/cliff" stop >/dev/null 2>&1 || true
elif [ -x "$INSTALL_DIR/stop.sh" ]; then
  DATA_DIR=data FORCE=1 sh "$INSTALL_DIR/stop.sh" >/dev/null 2>&1 || true
fi

# An existing Cliff install is upgraded in place and keeps its data and servers.
# Anything else in the way is left alone: this script never deletes a folder
# that is not a Cliff install.
only_user_data() {
  # True for a folder that holds nothing but a Cliff data and servers folder,
  # which is what is left after `cliff uninstall --keep-data`.
  for item in "$1"/* "$1"/.[!.]*; do
    [ -e "$item" ] || continue
    case "$(basename "$item")" in
      data|servers) ;;
      *) return 1 ;;
    esac
  done
  return 0
}

UPGRADE=0
REUSED_DATA=0
if [ -e "$INSTALL_DIR" ]; then
  if [ -f "$INSTALL_DIR/package-manifest.json" ]; then
    UPGRADE=1
  elif [ -d "$INSTALL_DIR" ] && [ -z "$(ls -A "$INSTALL_DIR" 2>/dev/null)" ]; then
    rmdir "$INSTALL_DIR"
  elif [ -d "$INSTALL_DIR" ] && only_user_data "$INSTALL_DIR"; then
    UPGRADE=1
    REUSED_DATA=1
  else
    echo "Refusing to install into $INSTALL_DIR: it exists and is not a Cliff install." >&2
    echo "Choose another folder with --install-dir, or remove it yourself." >&2
    exit 1
  fi
fi

mkdir -p "$(dirname "$INSTALL_DIR")"
if [ "$UPGRADE" = "1" ]; then
  # Remove the old program files; data/ and servers/ are never touched.
  for entry in "$INSTALL_DIR"/* "$INSTALL_DIR"/.[!.]*; do
    [ -e "$entry" ] || continue
    case "$(basename "$entry")" in
      data|servers) continue ;;
    esac
    rm -rf "$entry"
  done
  for entry in "$TEMP_ROOT/cliff"/* "$TEMP_ROOT/cliff"/.[!.]*; do
    [ -e "$entry" ] || continue
    name="$(basename "$entry")"
    case "$name" in
      data|servers)
        if [ -e "$INSTALL_DIR/$name" ]; then continue; fi
        ;;
    esac
    mv "$entry" "$INSTALL_DIR/$name"
  done
  if [ "$REUSED_DATA" = "1" ]; then
    echo "Installed Cliff in $INSTALL_DIR and kept the data and servers already there."
  else
    echo "Upgraded Cliff in $INSTALL_DIR (your data and servers were kept)."
  fi
else
  mv "$TEMP_ROOT/cliff" "$INSTALL_DIR"
fi

# Make the binary executable.
chmod +x "$INSTALL_DIR/cliff" 2>/dev/null || true

# Remember a custom data or servers folder. A folder that already holds Cliff
# data is reused as it is; one that does not exist yet is created.
if [ -n "$DATA_DIR_OPT" ] || [ -n "$SERVER_ROOT_OPT" ]; then
  set -- configure
  if [ -n "$DATA_DIR_OPT" ]; then set -- "$@" --data-dir "$DATA_DIR_OPT"; fi
  if [ -n "$SERVER_ROOT_OPT" ]; then set -- "$@" --server-root "$SERVER_ROOT_OPT"; fi
  "$INSTALL_DIR/cliff" "$@" || echo "Warning: could not save the data and server folders; start Cliff with --data-dir and --server-root instead." >&2
fi

path_contains() {
  case ":$PATH:" in
    *":$1:"*) return 0 ;;
  esac
  return 1
}

# Put the shell setup in a marked block so `cliff uninstall` can remove it again.
add_path_block() {
  rc="$1"
  mkdir -p "$(dirname "$rc")" 2>/dev/null || return 0
  [ -f "$rc" ] || : > "$rc"
  if grep -q '>>> cliff >>>' "$rc" 2>/dev/null; then
    return 0
  fi
  {
    echo ''
    echo '# >>> cliff >>>'
    echo 'export PATH="$HOME/.local/bin:$PATH"'
    echo '# <<< cliff <<<'
  } >> "$rc"
  echo "Added ~/.local/bin to PATH in $rc"
}

add_fish_path() {
  rc="$HOME/.config/fish/conf.d/cliff.fish"
  mkdir -p "$(dirname "$rc")" 2>/dev/null || return 0
  if [ ! -f "$rc" ]; then
    {
      echo '# cliff: put the cliff command on PATH'
      echo 'fish_add_path -g $HOME/.local/bin'
    } > "$rc"
    echo "Added ~/.local/bin to PATH for fish in $rc"
  fi
}

# Make `cliff` available. Prefer a folder that is already on PATH so it works in
# this very terminal; otherwise use ~/.local/bin and update the shell profile.
CLIFF_READY_NOW=0
setup_path_symlink() {
  binary="$INSTALL_DIR/cliff"

  for dir in /opt/homebrew/bin /usr/local/bin "$HOME/.local/bin" "$HOME/bin"; do
    if path_contains "$dir" && [ -d "$dir" ] && [ -w "$dir" ]; then
      # Do not replace a real file that something else installed.
      if [ -e "$dir/cliff" ] && [ ! -L "$dir/cliff" ]; then
        continue
      fi
      if ln -sf "$binary" "$dir/cliff" 2>/dev/null; then
        echo "Linked: $dir/cliff -> $binary"
        CLIFF_READY_NOW=1
        return 0
      fi
    fi
  done

  target="$HOME/.local/bin"
  if ! mkdir -p "$target" 2>/dev/null; then
    echo "Could not create $target. Use $INSTALL_DIR/cliff directly." >&2
    return 1
  fi
  if ! ln -sf "$binary" "$target/cliff" 2>/dev/null; then
    echo "Could not create a link at $target/cliff. Use $INSTALL_DIR/cliff directly." >&2
    return 1
  fi
  echo "Linked: $target/cliff -> $binary"

  if path_contains "$target"; then
    CLIFF_READY_NOW=1
    return 0
  fi

  case "${SHELL:-}" in
    */zsh) add_path_block "$HOME/.zshrc" ;;
    */bash)
      add_path_block "$HOME/.bashrc"
      if [ "$(uname -s)" = "Darwin" ]; then add_path_block "$HOME/.bash_profile"; fi
      ;;
    */fish) add_fish_path ;;
    *) add_path_block "$HOME/.profile" ;;
  esac
  return 0
}

setup_path_symlink || true

print_path_help() {
  if [ "$CLIFF_READY_NOW" = "1" ]; then
    return 0
  fi
  echo
  echo "To use 'cliff' in this terminal right now, run:"
  echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
  echo "New terminals pick it up automatically. Or run Cliff directly:"
  echo "  $INSTALL_DIR/cliff status"
}

if [ "$START" = "1" ]; then
  "$INSTALL_DIR/cliff" start -p "$PORT"
else
  echo "Cliff installed to $INSTALL_DIR"
  if [ "$CLIFF_READY_NOW" = "1" ]; then
    echo "Start it with: cliff start"
  else
    echo "Start it with: $INSTALL_DIR/cliff start"
  fi
fi
print_path_help
