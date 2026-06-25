#!/usr/bin/env sh
set -eu

if ! command -v node >/dev/null 2>&1; then
  echo "Node.js is required to check Cliff status. Install Node.js 22 or newer." >&2
  exit 1
fi

cd "$(dirname "$0")/.."
node scripts/status-daemon.mjs "$@"
