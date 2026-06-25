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

if [ -n "$MANIFEST" ]; then
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
      archive="$(sed -n 's/.*"file":[[:space:]]*"\([^"]*\.zip\)".*/\1/p' "$manifest_file" | head -n 1)"
      base="${MANIFEST%/*}/"
      PACKAGE="$base$archive"
      ;;
    *)
      manifest_file="$MANIFEST"
      archive="$(sed -n 's/.*"file":[[:space:]]*"\([^"]*\.zip\)".*/\1/p' "$manifest_file" | head -n 1)"
      PACKAGE="$(cd "$(dirname "$manifest_file")" && pwd)/$archive"
      ;;
  esac

  if [ -z "$archive" ]; then
    echo "Release manifest does not include archive.file." >&2
    exit 1
  fi
  EXPECTED_ARCHIVE_SHA256="$(sed -n 's/.*"sha256":[[:space:]]*"\([0-9a-fA-F][0-9a-fA-F]*\)".*/\1/p' "$manifest_file" | head -n 1 | tr 'A-F' 'a-f')"
fi

if [ -z "$PACKAGE" ]; then
  if [ -f "$ROOT/dist/cliff-release.json" ]; then
    archive="$(sed -n 's/.*"file":[[:space:]]*"\([^"]*\.zip\)".*/\1/p' "$ROOT/dist/cliff-release.json" | head -n 1)"
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
      curl -fsSL "$PACKAGE.sha256" -o "$TEMP_ROOT/cliff.zip.sha256" 2>/dev/null || true
    elif command -v wget >/dev/null 2>&1; then
      wget -qO "$TEMP_ROOT/cliff.zip" "$PACKAGE"
      wget -qO "$TEMP_ROOT/cliff.zip.sha256" "$PACKAGE.sha256" 2>/dev/null || true
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
        127.*|""|*:*|*.*.*.*.*) ;;
        *.*.*.*)
          echo "Same network: http://$address:$PORT"
          found=1
          ;;
      esac
    done
  fi
  if [ "$found" = "0" ]; then
    echo "Same network: no LAN IPv4 address detected"
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
require_extracted_file "run.sh"
require_extracted_file "status.sh"
require_extracted_file "stop.sh"

if [ -x "$INSTALL_DIR/stop.sh" ]; then
  DATA_DIR=data FORCE=1 sh "$INSTALL_DIR/stop.sh" >/dev/null 2>&1 || true
fi

if [ -e "$INSTALL_DIR" ] && [ "$FORCE" != "1" ]; then
  echo "Install directory already exists: $INSTALL_DIR. Re-run with --force to replace it." >&2
  exit 1
fi

rm -rf "$INSTALL_DIR"
mkdir -p "$(dirname "$INSTALL_DIR")"
mv "$TEMP_ROOT/cliff" "$INSTALL_DIR"

echo "Cliff installed."
echo "Path: $INSTALL_DIR"
echo "Local: http://localhost:$PORT"
print_lan_urls
echo "Run: PORT=$PORT sh $INSTALL_DIR/run.sh"
echo "Status: sh $INSTALL_DIR/status.sh"
echo "Stop: sh $INSTALL_DIR/stop.sh"

if [ "$START" = "1" ]; then
  PORT="$PORT" sh "$INSTALL_DIR/run.sh"
fi
