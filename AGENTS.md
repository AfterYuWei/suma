# DockPort Engineering Rules

## Scope

DockPort V2 is a monolithic, agentless multi-node Docker management application. Nodes connect only through mounted `unix://` sockets or direct `tcp://` Docker APIs. Do not implement remote agents, SSH execution, clusters, Swarm, Kubernetes, SSO, monitoring platforms, marketplaces, or automated backups. Record other ideas under Future in `PLANS.md`.

## Required stack

- Web: React 19, TypeScript, Vite, Tailwind CSS v4, shadcn/ui conventions, Base UI, TanStack Router, TanStack Query, Zustand, Lucide React, Motion, Monaco Editor, xterm.js, and ECharts.
- Server: Go, Gin, GORM, SQLite, Docker Go SDK, WebSocket, and the `docker compose` CLI.
- Deployment: one monolith container, persistent application data, Compose, and one or more mounted Unix sockets and/or mTLS Docker TCP endpoints.
- TCP defaults to mutual TLS. Plaintext TCP is permitted only for loopback endpoints; never expose an unauthenticated Docker API to a network. Do not add Redis, message brokers, microservices, Swarm, or Kubernetes.

## Architecture

- HTTP handlers call domain services; domain services call adapters; only adapters import the Docker SDK.
- Compose operations go through one `ComposeRunner`. Business packages must not call `exec.Command` directly.
- SQLite stores only DockPort-owned state. Container, image, network, volume, engine, and runtime status always come from Docker.
- Long operations use the Task service and stream progress. Important user operations use the Audit service.
- HTTP APIs use `/api/v1`; realtime endpoints use `/ws` and must cancel contexts and close Docker streams promptly on disconnect.
- Docker resources and Compose are always resolved through an explicit node runtime. CD and the Authentication Center remain global and may target multiple nodes.

## Frontend design

- Dark mode first, with dark/light/system themes and semantic design tokens.
- Prefer dense lists, inline/context actions, tabs, popovers, sheets, and progressive disclosure.
- Avoid dashboard card grids, crowded CRUD tables, blue-primary defaults, heavy shadows, gratuitous gradients, excessive rounding, and rows of action buttons.
- TanStack Query owns server state. Zustand owns only UI state such as theme, navigation, filters, preferences, and command-palette state.
- Shared primitives live in `web/src/components/ui`; Docker domain components live in `web/src/components/docker`.
- Command palette (`Cmd/Ctrl+K`) is a primary navigation and action surface.

## Security

- Use HttpOnly, SameSite session cookies and a strong password hash (bcrypt or Argon2id).
- Never log passwords, registry secrets, session tokens, or sensitive environment values. Mask likely secrets by default in the UI.
- Confirm destructive actions. Volume deletion must explicitly warn about data loss and require the volume name.
- Validate identifiers and paths. Compose project files must remain below the configured Compose root.
- TCP-node bind mounts must use non-interpolated absolute sources inside that node's allowlist. TLS and registry material must use short-lived private temporary files and never enter logs.

## Testing and progress

- `PLANS.md` is the progress source of truth. Check an item only after its listed verification passes.
- Server gate: `go test ./...` and `go build ./...`.
- Web gate: `npm run lint`, `npm run typecheck`, and `npm run build`.
- Tests should exercise services and HTTP behavior without requiring a live daemon. Run a separate real-Docker smoke test before final completion.
- Prefer simple, explicit composition. Avoid speculative abstractions, excessive interfaces, and repository layers with no behavior.
