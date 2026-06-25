# Cliff Daemon

The Go daemon (`cliff`) is the production runtime for Cliff. It serves the static dashboard, owns the HTTP API, manages the SQLite database, supervises Minecraft processes, and manages Java runtimes.

## Build

```bash
cd daemon
go mod download
go build ./cmd/cliff
```

## Run

```bash
./cliff --port 8080 --server-root ../servers --web-dir web
```

On Windows:
```powershell
.\cliff.exe --port 8080 --server-root ..\servers --web-dir web
```

## Flags & Environment Variables

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--host` | `CLIFF_HOST` | `0.0.0.0` | Host interface to bind |
| `--port` | `CLIFF_PORT` | `8080` | HTTP port |
| `--data-dir` | `CLIFF_DATA_DIR` | `.cliff` | Data directory (SQLite, logs, cache, Java) |
| `--server-root` | `CLIFF_SERVER_ROOT` | `servers` | Minecraft server storage root |
| `--web-dir` | `CLIFF_WEB_DIR` | `web` | Static dashboard directory |
| `--log-file` | `CLIFF_LOG_FILE` | `<data-dir>/logs/daemon.log` | Daemon log file |
| `--log-level` | `CLIFF_LOG_LEVEL` | `info` | Log level: debug, info, warn, error |
| `--version` | — | — | Print version, commit, and build time |

## API Routes

- `GET /api/health` — daemon health, build info, platform, uptime
- `GET /api/auth/me` — current session
- `POST /api/auth/setup` — create initial admin account
- `POST /api/auth/login` — authenticate
- `POST /api/auth/logout` — end session
- Settings, server CRUD, import/export, properties, players, files, worlds, backups, mods, Minecraft metadata, scheduler, public access
- `GET /api/servers/:id/console` — WebSocket console streaming and command input

## Internal Packages

| Package | Responsibility |
|---------|---------------|
| `cmd/cliff` | Entrypoint, flag parsing, server lifecycle |
| `internal/httpserver` | HTTP handlers, WebSocket console, static serving |
| `internal/process` | Minecraft process supervision (start, stop, signal) |
| `internal/store` | SQLite storage layer |
| `internal/java` | Managed Temurin Java runtime downloads |
| `internal/config` | Configuration resolution from flags and env vars |
| `internal/buildinfo` | Version, commit, and build time (set via ldflags) |
| `internal/logbuf` | In-memory log ring buffer for console streaming |

## Tests

```bash
cd daemon
go test ./...
```

## Architecture

The daemon is a single binary with no external runtime dependencies (CGO is disabled for cross-compilation). It uses:

- `modernc.org/sqlite` — pure-Go SQLite driver (no CGO required)
- `github.com/gorilla/websocket` — WebSocket for console streaming
- `gopkg.in/natefinch/lumberjack.v2` — log file rotation

Static dashboard assets are served from `web/` (or `CLIFF_WEB_DIR`). The daemon serves `index.html` as a fallback for deep links, and the client-side router handles the rest.
