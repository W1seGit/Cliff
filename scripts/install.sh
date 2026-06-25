#!/usr/bin/env sh
set -eu

DEFAULT_MANIFEST="https://github.com/W1seGit/Cliff/releases/latest/download/cliff-release.json"
INSTALLER_SOURCE="${CLIFF_INSTALL_PACKAGE_SH:-https://github.com/W1seGit/Cliff/releases/latest/download/install-package.sh}"
MANIFEST="${CLIFF_RELEASE_MANIFEST:-$DEFAULT_MANIFEST}"
PACKAGE=""
INSTALL_DIR="${CLIFF_INSTALL_DIR:-}"
PORT="${PORT:-8080}"
START=1
FORCE=0
SKIP_CHECKSUM=0

require_arg() {
  option="$1"
  if [ "$#" -lt 2 ] || [ -z "${2:-}" ]; then
    echo "Missing value for $option" >&2
    exit 1
  fi
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --manifest) require_arg "$1" "${2:-}"; MANIFEST="$2"; shift 2 ;;
    --package) require_arg "$1" "${2:-}"; PACKAGE="$2"; shift 2 ;;
    --install-dir) require_arg "$1" "${2:-}"; INSTALL_DIR="$2"; shift 2 ;;
    -p|--port) require_arg "$1" "${2:-}"; PORT="$2"; shift 2 ;;
    --no-start) START=0; shift ;;
    --force) FORCE=1; shift ;;
    --skip-checksum) SKIP_CHECKSUM=1; shift ;;
    -h|--help)
      echo "Usage: sh install.sh [--manifest json-or-url] [--package zip-or-url] [--install-dir path] [-p 8080|--port 8080] [--no-start] [--force] [--skip-checksum]"
      echo ""
      echo "After install, run: cliff start -p 8080"
      exit 0
      ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

LOCAL_INSTALLER=""
case "$0" in
  */*|.*)
    SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || pwd)"
    LOCAL_INSTALLER="$SCRIPT_DIR/install-package.sh"
    ;;
esac
TEMP_ROOT="$(mktemp -d)"
cleanup() {
  rm -rf "$TEMP_ROOT"
}
trap cleanup EXIT

if [ -n "$LOCAL_INSTALLER" ] && [ -f "$LOCAL_INSTALLER" ]; then
  INSTALLER="$LOCAL_INSTALLER"
else
  INSTALLER="$TEMP_ROOT/install-package.sh"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$INSTALLER_SOURCE" -o "$INSTALLER"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$INSTALLER" "$INSTALLER_SOURCE"
  else
    echo "curl or wget is required to download the Cliff installer." >&2
    exit 1
  fi
  chmod +x "$INSTALLER"
fi

if [ -n "$PACKAGE" ]; then
  set -- --package "$PACKAGE"
else
  set -- --manifest "$MANIFEST"
fi
if [ -n "$INSTALL_DIR" ]; then
  set -- "$@" --install-dir "$INSTALL_DIR"
fi
set -- "$@" --port "$PORT"
if [ "$START" = "1" ]; then
  set -- "$@" --start
fi
if [ "$FORCE" = "1" ]; then
  set -- "$@" --force
fi
if [ "$SKIP_CHECKSUM" = "1" ]; then
  set -- "$@" --skip-checksum
fi

sh "$INSTALLER" "$@"
