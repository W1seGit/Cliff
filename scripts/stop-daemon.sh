#!/usr/bin/env sh
set -eu

if ! command -v node >/dev/null 2>&1; then
  echo "Node.js is required to stop Cliff with this script. Install Node.js 22 or newer, or stop the cliff process manually." >&2
  exit 1
fi

cd "$(dirname "$0")/.."
node scripts/stop-daemon.mjs "$@"
