# Daemon Static Runtime

Cliff's production runtime is a Go daemon that serves a static dashboard. The dashboard is built with Next.js static export and talks to same-origin daemon APIs.

## Runtime Shape

```text
Browser
  -> static HTML/CSS/JS from Go daemon
  -> HTTP API and WebSocket console on the same daemon
Go daemon (cliff)
  -> SQLite state
  -> managed Java runtimes
  -> Minecraft server processes
```

There is no production Next.js server. Production packages must not contain `.next`, `node_modules`, `src`, `scripts`, or daemon source files.

## Source Boundaries

- Frontend code lives under `src/app`.
- Dashboard API calls go through `src/app/dashboard/lib/runtime-client.ts`.
- Shared frontend response types live under `src/app/dashboard/lib/types.ts`.
- Backend behavior lives under `daemon/internal`.
- New HTTP, WebSocket, storage, process, file, mod, backup, Java, and scheduler behavior belongs in Go.
- `src/app/api` and `src/lib` must not exist in the daemon-only runtime.

## Install Paths

Release users should install from GitHub release assets:

```powershell
irm getcliff.dev/install.ps1 | iex
```

```bash
curl -fsSL getcliff.dev/install.sh | sh
```

The release manifest points to:

- `cliff-<version>-<platform>-<arch>.zip`
- `install.ps1`
- `install.sh`
- `install-package.ps1`
- `install-package.sh`

Every installer artifact in the release manifest includes its file name, byte size, and SHA-256 hash.

Source installs are for development and local package generation:

```bash
npm run install:run
```

## Footprint Targets

The daemon should stay small enough that Minecraft gets the resources:

- package folder: under 100 MB before user data
- release archive: under 50 MB
- static web assets: under 8 MB
- static JavaScript: under 4 MB
- idle daemon allocated heap: under 32 MB
- idle daemon reserved heap: under 96 MB
- daemon working set in install/start smokes: under 100 MB

## Verification Gates

Run the default daemon verification before release:

```bash
npm run verify
```

Focused checks:

```bash
npm run verify:daemon-defaults
npm run smoke:daemon-web
npm run smoke:daemon-package
npm run smoke:install-package
npm run smoke:install-run
npm run smoke:start
npm run smoke:daemon
```

The guardrails enforce:

- production scripts launch packaged `cliff`, not `go run` or a Next server
- frontend console uses WebSocket, not SSE
- `src/app/api` and `src/lib` do not come back
- static export has no legacy runtime bundle paths
- release archives and manifests include installer artifacts
- release manifests verify installer artifact sizes and SHA-256 hashes
- release manifests, archive metadata, and checksum sidecars agree
- package installers verify archives from the release manifest hash when the checksum sidecar is unavailable
- package and archive runner scripts pass daemon log and server-root flags directly
- static web, package, and archive outputs include bundled public logos and fallback player head assets
- package/archive sizes remain within budget
- startup paths print local and same-network dashboard URLs
