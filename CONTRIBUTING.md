# Contributing to Cliff

Thanks for your interest in contributing! This guide covers setup, code style, and pull request workflow.

## Prerequisites

- [Node.js](https://nodejs.org/) 22 or newer
- [Go](https://go.dev/dl/) 1.23 or newer
- [Git](https://git-scm.com/)
- [Chrome](https://www.google.com/chrome/) or [Edge](https://www.microsoft.com/edge) (for UI smoke tests)

## Setup

```bash
git clone https://github.com/W1seGit/Cliff.git
cd Cliff
npm install
```

## Development

### Frontend (Next.js dev server)

```bash
npm run dev
```

For LAN-accessible development with hot reload from other devices:

```bash
npm run dev:lan
```

Open `http://localhost:3000`. The first visit creates the local admin account.

### Daemon (Go)

Build the static web assets, then run the daemon:

```bash
npm run build:daemon-web
npm run daemon:run
```

Open `http://localhost:8080`.

### Both together (production-like)

```bash
npm run build
npm start
```

This builds the full package and starts the packaged daemon.

## Code Style

### Frontend (TypeScript / React)

- Follow existing patterns in `src/app/`.
- Use functional components with hooks.
- Keep styles in the existing CSS files under `src/app/styles/`.
- Use `lucide-react` for icons.
- API calls go through `src/app/dashboard/lib/runtime-client.ts`.
- Shared types live in `src/app/dashboard/lib/types.ts`.
- Do not add `src/app/api` or `src/lib` — all backend behavior belongs in the Go daemon.

### Backend (Go)

- Follow standard Go conventions (`gofmt`, `go vet`).
- New HTTP handlers go in `daemon/internal/httpserver/`.
- New storage logic goes in `daemon/internal/store/`.
- Process management goes in `daemon/internal/process/`.
- Use `slog` for logging.
- No CGO — the daemon must cross-compile cleanly with `CGO_ENABLED=0`.

### Scripts (Node.js)

- Build/package/install scripts live in `scripts/`.
- Use `.mjs` (ES modules).
- Follow existing patterns for argument parsing and error handling.

## Testing

### Go tests

```bash
npm run daemon:test
```

### Full verification suite

```bash
npm run verify
```

This runs lint, Go tests, daemon verification, and all smoke tests. Run this before submitting a pull request.

### Individual checks

```bash
npm run lint
npm run smoke:daemon
npm run smoke:ui
npm run smoke:auth
npm run verify:minecraft-meta
```

## Pull Request Workflow

1. **Fork** the repository and create a branch from `master`.
2. **Make your changes** following the code style above.
3. **Run the full verification suite**: `npm run verify`.
4. **Commit** with clear, descriptive messages.
5. **Open a pull request** against `master` with a summary of what changed and why.

### Commit messages

Use the imperative mood: "Add Fabric modpack import" not "Added Fabric modpack import".

Reference issues when relevant: "Fix port drift detection (#42)".

### What to avoid

- Do not add dependencies without justification — keep the package small.
- Do not introduce CGO dependencies in the Go daemon.
- Do not add `src/app/api` routes — all API behavior belongs in the Go daemon.
- Do not commit `.next/`, `node_modules/`, `dist/`, `.cliff/`, or `daemon/web/` (except `index.html`).

## Project Structure

```
src/app/              Next.js frontend
  dashboard/          Dashboard pages, components, styles
  lib/                API client, types, utilities
daemon/               Go daemon
  cmd/cliff/          Entrypoint
  internal/           HTTP, process, store, java, config, etc.
  web/                Built static assets (generated, gitignored except index.html)
scripts/              Build, package, install, smoke, verify scripts
docs/                 Architecture docs
.github/workflows/    CI/CD
```

## License

By contributing, you agree that your contributions will be licensed under the [GPL v3](LICENSE).
