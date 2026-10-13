# SUMA Engineering Rules

## Scope

SUMA is a monolithic multi-node Docker management control plane. Nodes connect through mounted `unix://` sockets, direct `tcp://` Docker APIs, or an outbound HTTPS/WSS Agent that forwards the local Docker socket. The Agent does not run Compose or arbitrary host commands. Do not implement SSH execution, clusters, Swarm, Kubernetes, SSO, monitoring platforms, marketplaces, or automated backups. Record other ideas under Future in `PLANS.md`.

## Required stack

- Web: React 19, TypeScript, Vite, Tailwind CSS v4, shadcn/ui conventions, Base UI, TanStack Router, TanStack Query, Zustand, Lucide React, Motion, Monaco Editor, xterm.js, and ECharts.
- Server: Go, Gin, GORM, PostgreSQL 18.6, Docker Go SDK, WebSocket, and the `docker compose` CLI. SQLite is not supported. Empty development/test databases are initialized fresh; production upgrades preserve existing data through versioned incremental migrations.
- Deployment: one control-plane container with persistent application files, a PostgreSQL service, Compose, and optional Agent containers on remote Docker hosts. Unix sockets and mTLS Docker TCP endpoints remain supported.
- TCP defaults to mutual TLS. Plaintext TCP is permitted only for loopback, private-network, or Tailscale IP endpoints and requires typed endpoint confirmation; never expose an unauthenticated Docker API to a public network. Do not add Redis, message brokers, microservices, Swarm, or Kubernetes.

## Architecture

- HTTP handlers call domain services; domain services call adapters; only adapters import the Docker SDK.
- Compose operations go through one `ComposeRunner`. Business packages must not call `exec.Command` directly.
- PostgreSQL stores only SUMA-owned state, task checkpoints and historical evidence. Container, image, network, volume, engine, and runtime status always come from Docker.
- Incoming bot messages only discover notification recipient metadata; they must not read Docker, execute commands, reply to queries, bind identities, or process approvals.
- Long operations use the Task service and stream progress. Important user operations use the Audit service.
- HTTP APIs use `/api/v1`; realtime endpoints use `/ws` and must cancel contexts and close Docker streams promptly on disconnect.
- Docker resources and Compose are always resolved through an explicit node runtime. CD and the Authentication Center remain global and may target multiple nodes.

## Frontend design

- Dark mode first, with dark/light/system themes and semantic design tokens.
- Prefer dense lists, inline/context actions, tabs, popovers, sheets, and progressive disclosure.
- Group functional sections with subtle theme-aware background fills. Separate resource-list rows with spacing, alignment, and hover states; use filled active tabs. Avoid thin-line section dividers, row separators, and tab underlines.
- Avoid dashboard card grids, crowded CRUD tables, blue-primary defaults, heavy shadows, gratuitous gradients, excessive rounding, and rows of action buttons.
- TanStack Query owns server state. Zustand owns only UI state such as theme, navigation, filters, preferences, and command-palette state.
- Shared primitives live in `web/src/components/ui`; Docker domain components live in `web/src/components/docker`.
- Command palette (`Cmd/Ctrl+K`) is a primary navigation and action surface.

## Security

- Use HttpOnly, SameSite session cookies and a strong password hash (bcrypt or Argon2id).
- Never log passwords, registry secrets, session tokens, or sensitive environment values. Mask likely secrets by default in the UI.
- Confirm destructive actions. Volume deletion must explicitly warn about data loss and require the volume name.
- Validate identifiers and paths. Compose project files must remain below the configured Compose root.
- TCP and Agent node bind mounts must use non-interpolated absolute sources. TLS and registry material must use short-lived private temporary files and never enter logs.
- Agent enrollment tokens expire after ten minutes and are single-use. Store only token and Agent credential hashes in PostgreSQL; never log either secret. Agent transport requires verified HTTPS/WSS. Treat the mounted Docker socket as full Engine control even when mounted read-only.

## Testing and progress

- `PLANS.md` is the progress source of truth. Check an item only after its listed verification passes.
- Scale verification to the change. Small, localized changes (such as copy, styling, tooltip layout, or documentation) need only the necessary targeted checks for the affected area; do not run full browser regression, both Go/Web gates, or real-Docker smoke tests for these changes. Large changes (such as new features, architecture, cross-cutting behavior, or schema changes) require full browser regression, both gates below, and a separate real-Docker smoke test before completion.
- Full server gate: `go test ./...` and `go build ./...`.
- Full Web gate: `npm run lint`, `npm run typecheck`, and `npm run build`.
- Tests exercise services and HTTP behavior against an isolated PostgreSQL schema with controlled runtime/provider substitutes, without requiring a live Docker daemon. Configure SUMA_TEST_DATABASE_DSN in root .env.local or the process environment; native startup and tests read the private file without shell evaluation. Do not skip or fall back to an embedded database.
- Prefer simple, explicit composition. Avoid speculative abstractions, excessive interfaces, and repository layers with no behavior.

## Production data and schema upgrades

- Production is live as of 2026-10-11. Preserve accounts, nodes, credentials, settings, task/audit history, notification rules/subscriptions/delivery evidence, application files and the existing encryption key across upgrades.
- Published migrations, including `0001_initial.sql`, are immutable. Add new numbered SQL migrations for schema/data changes using the existing transaction, advisory lock and migration ledger. Never rewrite an applied baseline, delete ledger entries or require a fresh production database to upgrade.
- Database resets, data/volume deletion and encryption-key replacement require explicit approval for that specific destructive action. Earlier development resets or one-off migration approvals do not authorize future production resets.
- Prepare conversion/backfill, validation and recovery steps before changing existing data. Preserve historical evidence; do not fabricate old policy snapshots or silently choose among conflicting configurations.
- Database-change tests must cover both empty initialization and upgrades from the supported previous schema, including retained IDs, credentials, settings and queued/historical evidence. Development/test schemas may remain disposable; production data may not.
