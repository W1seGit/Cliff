#!/bin/bash
# install-playit-deps-macos.sh
# Installs the build prerequisites for the playit.gg agent on macOS.
# Only missing dependencies are installed; present ones are skipped.
#
# This script is embedded into the Cliff daemon via go:embed and executed by
# the playit build manager. It prints step markers the daemon parses:
#
#   [cliff:dep] <name> installing
#   [cliff:dep] <name> done
#   [cliff:dep] <name> skipped (already installed)
#   [cliff:done]
#   [cliff:error] <message>
#
# Dependencies handled:
#   - git          (provided by Xcode Command Line Tools)
#   - xcode-clt    (Xcode Command Line Tools, provides cc/git)
#   - rust         (via rustup, installs into ~/.cargo)
set -euo pipefail

log_dep_installing() { printf '[cliff:dep] %s installing\n' "$1"; }
log_dep_done()       { printf '[cliff:dep] %s done\n' "$1"; }
log_dep_skipped()    { printf '[cliff:dep] %s skipped (already installed)\n' "$1"; }
log_done()           { printf '[cliff:done]\n'; }
log_error()          { printf '[cliff:error] %s\n' "$1" >&2; }

trap 'log_error "dependency install failed"; exit 1' ERR

# --- Xcode Command Line Tools (provides git and cc) -----------------------
if ! xcode-select -p >/dev/null 2>&1; then
  log_dep_installing "xcode-clt"
  # xcode-select --install triggers a GUI prompt. The user must confirm it.
  # We start the install and wait for the command line tools to become available.
  xcode-select --install 2>/dev/null || true
  echo "  Waiting for Xcode Command Line Tools install dialog — click Install in the popup."
  echo "  This may take several minutes. If the dialog does not appear, the tools may already be installing."
  # Poll for up to ~15 minutes for the tools to land.
  for _ in $(seq 1 180); do
    if xcode-select -p >/dev/null 2>&1; then
      break
    fi
    sleep 5
  done
  if ! xcode-select -p >/dev/null 2>&1; then
    log_error "Xcode Command Line Tools were not installed. Please run 'xcode-select --install' manually and retry."
    exit 1
  fi
  log_dep_done "xcode-clt"
else
  log_dep_skipped "xcode-clt"
fi

# --- git ------------------------------------------------------------------
if command -v git >/dev/null 2>&1; then
  log_dep_skipped "git"
else
  log_dep_installing "git"
  # On macOS, git comes from the CLT. If it's still missing, try Homebrew.
  if command -v brew >/dev/null 2>&1; then
    brew install git
  else
    log_error "git is missing and Homebrew is not available. Install Xcode Command Line Tools first."
    exit 1
  fi
  log_dep_done "git"
fi

# --- Rust (cargo + rustc via rustup) --------------------------------------
if command -v cargo >/dev/null 2>&1 && command -v rustc >/dev/null 2>&1; then
  log_dep_skipped "rust"
else
  log_dep_installing "rust"
  # rustup installs into ~/.cargo/bin and modifies shell profiles.
  curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal
  # Source it so cargo is available in this same process for the build step.
  # shellcheck disable=SC1091
  . "$HOME/.cargo/env"
  if ! command -v cargo >/dev/null 2>&1; then
    log_error "rustup finished but cargo is not on PATH. Check ~/.cargo/env and retry."
    exit 1
  fi
  log_dep_done "rust"
fi

log_done
