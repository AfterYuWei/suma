# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

DockPort is an agentless multi-node Docker management application: one Go monolith (`server/`) serving a React 19 SPA (`web/`), versioned REST APIs under `/api/v1`, and WebSockets under `/ws`. Nodes are reached only through mounted `unix://` sockets or direct `tcp://` Docker APIs — never agents, SSH execution, Swarm, or Kubernetes.

Authoritative docs, in order of usefulness: `AGENTS.md` (engineering rules — read it, they are binding), `ARCHITECTURE.md`, `CD-DESIGN.md`, `SEMI-MIGRATION.md` (frontend styling boundary), `API.md`, `PLANS.md` (progress source of truth), `README.md` (env vars, deployment).

## Commands

```bash
make install          # npm ci + go mod download
make dev              # Vite on 0.0.0.0:5173 + Go on :8081 (dev ports avoid the :8080 prod container)
make check            # all gates: web lint/typecheck/build + go test/build
make web-check        # npm run lint && npm run typecheck && npm run build
make server-check     # go test ./... && go build -buildvcs=false ./...
make docker-up        # build + run the production container (port 8080)
make help             # full command list
```

Overrides: `make dev DEV_HOST=0.0.0.0 DEV_WEB_PORT=3000 DEV_API_PORT=9080`.

Single Go test / package:

```bash
cd server && GOCACHE=/tmp/dockport-go-cache go test ./internal/cd/ -run TestReleasePolicy -v
```

Real-Docker smoke tests are behind a build tag and excluded from `go test ./...`:

```bash
cd server && go test -tags dockersmoke ./internal/cd/ ./internal/node/
```

Frontend extra gate (not in `make check` — run it after any UI work):

```bash
cd web && npm run audit:universe
```

Always use `-buildvcs=false` for Go builds. Lint is `oxlint`, not ESLint. There is no frontend test runner; frontend verification is lint + typecheck + build + browser smoke checks.

## Server layering (enforced, not aspirational)

`api` handlers → domain services → adapters. Violating these boundaries is the main correctness risk in this codebase:

- `internal/docker/adapter.go` is the **only** place that imports the Docker SDK. Handlers never see Docker SDK types.
- `internal/compose/runner.go` is the **only** Compose process boundary. No business package calls `exec.Command`.
- `internal/git` is the only Git process and Git-credential boundary.
- `internal/api/router.go` (~1200 lines) wires every route and transport concern; new endpoints go here.
- `internal/app/app.go` composes services at startup.

SQLite (GORM) stores **only** DockPort-owned state: users, sessions, settings, node definitions, credential grants, Compose/CD metadata, releases, tasks, audit records. Container/image/network/volume/status data is always read live from Docker — never mirror it into SQLite.

Every Docker resource operation resolves through an explicit node runtime. A runtime client is captured when work starts, so disabling/updating a node blocks new work without killing in-flight tasks. Long operations go through `internal/task` (pending/running/success/failed/canceled + streamed logs); user-visible mutations go through `internal/audit`. WebSocket handlers (logs, stats, exec) must cancel their context and close the Docker stream on disconnect.

## Compose vs. Continuous Delivery

These are two independent aggregates and conflating them is a recurring design mistake:

- **Compose project** (`internal/compose`): editable `compose.yml`/`.env` below the Compose root, node-scoped, local orchestration only.
- **Delivery Project** (`internal/cd`): global, owns repo config, sync policy, credentials, releases, approvals, multi-node deployment, drift, rollback. Git-sourced Compose files are read-only in the Compose UI.

Creating or deleting one never touches the other. Compose is merely a deployment adapter invoked by a release. CD is deliberately one-way and never builds source, runs tests, publishes images, or executes repository scripts. Releases pin an exact commit + canonical config hash; approval is a state transition that does not itself deploy; rollback creates a new release and flips an `auto` project to `manual`. `internal/cd/source_policy.go` and `policy.go` enforce a strict Compose policy (rejects `build`, `include`/`extends`, privileged, host namespaces, socket mounts, writable/external binds, interpolated paths…). `internal/compose/bind_policy.go` enforces per-node absolute bind-source allowlists.

## Frontend

`web/src/`: `pages/` (route views), `features/<domain>/` (composed domain UI), `components/shell` + `components/ui` (shared primitives), `stores/` (Zustand), `lib/` (api client, i18n, node helpers).

- TanStack Query owns all server state; **every Docker-resource query key must include `node_id`**. CD and Authentication Center queries are global. Zustand holds only UI state (theme, selected node, filters, palette, dialogs).
- Semi Design (`@douyinfe/semi-ui-19`) with the Feishu Universe Design theme (`@semi-bot/semi-theme-universedesign`, compiled via `@douyinfe/semi-vite-plugin`) owns **all** appearance: colors, typography, radii, borders, shadows, density, states, motion.
- Project code may own only responsive structure (display, grid/flex, placement, sizing, gap, overflow, breakpoints), Docker information architecture, and Monaco/xterm/ECharts configuration.
- Forbidden and mechanically checked by `npm run audit:universe`: `.semi-*` selector overrides, literal colors in CSS, and visual Tailwind utilities in `className` (`bg-*`, text/border colors, `rounded-*`, `shadow-*`, `font-*`, gradients…). Tailwind is layout-only. ECharts colors come from `--semi-color-data-*`.
- Note `AGENTS.md` still lists shadcn/Base UI in the stack; that has been superseded by the Semi/Universe migration described in `SEMI-MIGRATION.md`.
- Dark-first with dark/light/system themes; Chinese/English localization is persistent; `Cmd/Ctrl+K` command palette is a primary navigation surface. Prefer dense lists, inline/context actions, tabs, sheets, progressive disclosure — not dashboard card grids or rows of action buttons.

## Security invariants

Sessions are opaque, hashed server-side, in HttpOnly SameSite cookies; passwords are bcrypt; the first user becomes administrator. Secrets (Git/registry credentials, SSH keys, Docker TLS material, webhook secrets) are AES-GCM encrypted in SQLite under the key at `DOCKPORT_SECRET_KEY_FILE` (mode `0600`) — losing it makes stored credentials unrecoverable. Credential material is passed to subprocesses through `0700` temp dirs / `0600` files removed on completion, failure, or cancellation. Never log passwords, tokens, secrets, or sensitive env values; mask likely secrets in API/UI output. Destructive actions require explicit confirmation and are audited — volume deletion requires typing the volume name. Validate identifiers and paths: Compose files stay under the Compose root, delivery files stay inside the detached worktree after symlink resolution. Clone URLs are HTTPS/SSH only, no embedded passwords. Plaintext Docker TCP is rejected unless loopback.

## Working conventions

`PLANS.md` is the progress record — check an item only after its stated verification actually passes, and log out-of-scope ideas under Future rather than implementing them. Tests should exercise services and HTTP behavior without a live daemon; keep live-Docker coverage in the `dockersmoke`-tagged files. Prefer simple explicit composition over speculative interfaces or behavior-free repository layers.
