#!/usr/bin/env sh
set -eu

if ! command -v node >/dev/null 2>&1; then
  echo "Node.js is required before installing Cliff. Install Node.js 22 or newer, then run this command again." >&2
  exit 1
fi

node_major="$(node --version | sed 's/^v//' | cut -d. -f1)"
if [ "${node_major:-0}" -lt 22 ]; then
  echo "Node.js 22 or newer is required before installing Cliff. Found $(node --version)." >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required before installing Cliff daemon. Install Go 1.22 or newer, then run this command again." >&2
  exit 1
fi

go_version="$(go version | sed -n 's/.* go\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p')"
if [ -z "$go_version" ]; then
  echo "Could not determine Go version from: $(go version)" >&2
  exit 1
fi
go_major="$(printf '%s\n' "$go_version" | awk '{print $1}')"
go_minor="$(printf '%s\n' "$go_version" | awk '{print $2}')"
if [ "$go_major" -lt 1 ] || { [ "$go_major" -eq 1 ] && [ "$go_minor" -lt 22 ]; }; then
  echo "Go 1.22 or newer is required before installing Cliff daemon. Found go$go_major.$go_minor." >&2
  exit 1
fi

cd "$(dirname "$0")/.."
node scripts/install-run.mjs "$@"
