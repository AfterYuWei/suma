# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

SUMA is an agentless multi-node Docker management application: one Go monolith (`server/`) serving a React 19 SPA (`web/`), versioned REST APIs under `/api/v1`, and WebSockets under `/ws`. Nodes are reached only through mounted `unix://` sockets or direct `tcp://` Docker APIs — never agents, SSH execution, Swarm, or Kubernetes.

Authoritative docs, in order of usefulness: `AGENTS.md` (engineering rules — read it, they are binding), `ARCHITECTURE.md`, `CD-DESIGN.md`, `API.md`, `PLANS.md` (progress source of truth), `README.md` (Chinese; `doc/README.en.md` is the English copy — env vars, deployment), `LAUNCH_REPORT.md` (pre-launch verification status).

Gitignored local workspaces you will see on disk but must not treat as part of the build: `Page/` (a separate Cloudflare landing-page project), `server/data/` (bare-metal dev data root), `server/bin/`, `web/dist/`.

## Commands

```bash
make install          # npm ci + go mod download
make dev              # Vite on 0.0.0.0:5173 + Go on :8081 (dev ports avoid the :8080 prod container)
make check            # all gates: web lint/typecheck/build + go test/build
make web-check        # npm run lint && npm run typecheck && npm run build
make server-check     # go test ./... && go build -buildvcs=false ./...
make build            # web-build + server-build (binary at server/bin/suma) — see embedding note below
make docker-up        # build + run the production container (port 8080)
make help             # full command list
```

Overrides: `make dev DEV_HOST=0.0.0.0 DEV_WEB_PORT=3000 DEV_API_PORT=9080`. Vite proxies `/api` and `/ws` to `SUMA_DEV_API` (default `http://127.0.0.1:8081`). The dev server runs with cwd `server/`, so `SUMA_DATA_ROOT` defaults to `server/data`. Every `SUMA_*` env var and its default is in `server/internal/config/config.go`.

Single Go test / package (a real `-run` prefix, matches two policy tests):

```bash
cd server && GOCACHE=/tmp/suma-go-cache go test ./internal/cd/ -run TestValidateDeploymentPolicy -v
```

Real-Docker smoke tests are behind a build tag **and** an env gate; without `SUMA_RUN_DOCKER_SMOKE=1` they skip even with the tag. They live in `cd`, `compose`, `docker`, `image`, and `node`:

```bash
cd server && SUMA_RUN_DOCKER_SMOKE=1 go test -tags dockersmoke -count=1 ./internal/cd ./internal/compose ./internal/docker ./internal/image ./internal/node
# mTLS TCP node smoke additionally needs SUMA_SMOKE_TCP_HOST=tcp://... and SUMA_SMOKE_TLS_DIR=<ca.pem/cert.pem/key.pem dir>
```

Demo build (public showcase, no Go server or Docker needed): `cd web && npm run dev:demo` / `npm run build:demo`. Vite `--mode demo` sets `VITE_SUMA_DEMO_MODE=true`; `web/src/lib/api.ts` then dynamically imports `web/src/lib/mock-api.ts` (login `admin` / `admin123`). A normal `npm run build` must tree-shake the whole mock module — never import `mock-api.ts` statically.

Web asset embedding: the Go binary serves `server/webui/dist` via `//go:embed` in `server/webui/web.go`. Only a placeholder `index.html` is tracked there so `go build` works without a web build. The Dockerfile copies `web/dist` into it; `make build` does **not**, so a bare `server/bin/suma` shows the placeholder page unless you copy `web/dist/*` into `server/webui/dist/` yourself. Do not commit built assets there.

Always use `-buildvcs=false` for Go builds. Lint is `oxlint`, not ESLint. There is no frontend test runner; frontend verification is lint + typecheck + build + browser smoke checks. GitHub Actions only build/push images (`pre` on main pushes, `stable` on `v*` tags) and create releases — nothing in CI runs tests, so `make check` locally is the only gate.

## Server layering (enforced, not aspirational)

`api` handlers → domain services → adapters. Violating these boundaries is the main correctness risk in this codebase:

- `internal/docker/adapter.go` is the **only** non-test file that imports the Docker SDK. It implements the `Service` interfaces declared by the domain packages (`container`, `image`, `network`, `volume`, `compose`) and imports them — so those domain packages must never import `internal/docker` (import cycle). Handlers see only adapter-owned plain types such as `docker.Engine` and `docker.Info`.
- `internal/compose/runner.go` is the **only** Compose process boundary. No business package calls `exec.Command`.
- `internal/git` is the only Git process and Git-credential boundary.
- `internal/api/router.go` (~1600 lines) wires global routes and transport concerns (`requireAuth`, session cookie `suma_session`, security headers, WebSocket upgrade). Node-scoped resource routes live in `internal/api/nodes.go` as `registerNode*Routes` functions under `/api/v1/nodes/:nodeID/{containers,images,networks,volumes,compose,projects}`; fleet aggregation is in `fleet.go`. Non-node paths like `/api/v1/containers` and `/ws/containers` are legacy default-node aliases guarded by `deprecatedDefaultNode()` — new endpoints go under the node scope, never as new aliases.
- Every response uses the envelope `{code, message, data}` through `success()`/`failure()` in `response.go`; `code` 0 is success and failure codes are 5-digit per domain (11xxx auth, 20xxx nodes, ...). The web client throws `ApiError` on any non-zero code.
- `api.Dependencies` fields are optional and nil-guarded (`if deps.Nodes != nil { ... }`), which is how tests build a router with only the services under test.
- `internal/app/app.go` composes services at startup and starts the background loops: CD database-only recovery, node status probes every 30s (`node/service.go`), the CD reconciler every 15s (`cd/reconciler.go`), and shadow-preview recovery across enabled nodes.

SQLite (GORM, `internal/database`) stores **only** SUMA-owned state: users, sessions, settings, node definitions, credential grants, Compose/CD metadata, releases, tasks, audit records. Container/image/network/volume/status data is always read live from Docker — never mirror it into SQLite.

Every Docker resource operation resolves through an explicit node runtime. A runtime client is captured when work starts, so disabling/updating a node blocks new work without killing in-flight tasks. Long operations go through `internal/task` (pending/running/success/failed/canceled + streamed logs, scoped `control_plane` or `node`); user-visible mutations go through `internal/audit`. WebSocket handlers (logs, stats, exec, task output) must cancel their context and close the Docker stream on disconnect.

Tests: the router tests use a `fakeEngine` implementing `docker.Engine`, a `t.TempDir()` SQLite file via `database.Open`, and `secret.Open` on a temp key; the adapter tests run against an `httptest` fake Docker API. Follow that pattern rather than reaching for a live daemon.

## Projects, Compose, and Continuous Delivery

`Project` (`internal/project/model.go`: `Ref`, `Summary`, `Capability`, backend/scope) is SUMA's first-level orchestration object. Today the only backend is `compose`, which maps to an official Docker Compose Project discovered by grouping `com.docker.compose.project` labels; `internal/compose` implements it. The UI has one Projects entry at `/projects/$backend/$projectName` (served by `pages/compose.tsx` and `pages/compose-detail.tsx`; `/compose` routes redirect there). Capability-driven actions distinguish SUMA-managed projects (editable files below the Compose root) from external projects (takeover, cleanup). No Swarm code exists; log Swarm ideas under Future in `PLANS.md`.

Compose and CD are two independent aggregates and conflating them is a recurring design mistake:

- **Compose project** (`internal/compose`): editable `compose.yml`/`.env`/`.suma/project.json` below the Compose root, node-scoped, local orchestration only. Takeover saves configuration atomically and never pulls, stops, or recreates containers.
- **Delivery Project** (`internal/cd`): global, owns repo config, sync policy, credentials, releases, approvals, multi-node deployment, drift, rollback. Git-sourced Compose files are read-only in the Compose UI.

Creating or deleting one never touches the other. Compose is merely a deployment adapter invoked by a release. CD is deliberately one-way and never builds source, runs tests, publishes images, or executes repository scripts. Releases pin an exact commit + canonical config hash; approval is a state transition that does not itself deploy; rollback creates a new release and flips an `auto` project to `manual`. `internal/cd/source_policy.go` and `policy.go` enforce a strict Compose policy (rejects `build`, `include`/`extends`, privileged, host namespaces, socket mounts, writable/external binds, interpolated paths…). `internal/compose/bind_policy.go` requires remote bind sources to be explicit absolute paths.

## Frontend

`web/src/`: `pages/` (route views), `features/<domain>/` (auth, containers, compose, delivery — domain components plus a `types.ts` holding that domain's API types), `components/shell` (app shell, command palette), `components/ui` (shadcn primitives and shared wrappers), `stores/` (Zustand), `lib/` (api client, i18n, node helpers). There is no `components/docker` directory; Docker domain UI belongs in `features/`.

- Routes are code-defined in `web/src/app.tsx` with TanStack Router `createRoute`; detail pages are lazy-loaded. Add new routes there.
- `api<T>(path)` in `lib/api.ts` prefixes `/api/v1`, unwraps the envelope, and throws `ApiError`. Node-scoped paths are built with `nodePath(nodeID, '/containers')` from `lib/nodes.ts`, with `nodeID` read from `useUIStore((s) => s.currentNodeID)`.
- TanStack Query owns all server state; **every Docker-resource query key must include `node_id`** (`['containers', nodeID]`) so switching nodes never shows another node's data. CD and Authentication Center queries are global. Resource pages poll with `refetchInterval`; the client default is `staleTime: 5_000, retry: 1`.
- Zustand holds only UI state. `stores/ui.ts` persists theme, language, current node, log tail, and list page size by hand under `suma-*` localStorage keys and applies theme via the `dark` class and `data-theme` on `documentElement`. `stores/dialog.ts` provides promise-based `confirmDialog` / `promptDialog` / `promptWithCheckboxDialog` / `choiceDialog`, rendered once by `components/ui/app-dialog.tsx`; destructive flows use `danger: true`, and typed confirmations use `input.requiredValue`.
- i18n: `useI18n()` returns `{ t, language }`. Shared keys live in `lib/i18n.ts`, where the English table is typed `Record<TranslationKey, string>` against the Chinese object, so a key missing in either language fails typecheck. Page-local copy uses `const zh = language === 'zh-CN'` and inline `zh ? '中文' : 'English'` ternaries. Every user-visible string must exist in both languages.
- Shared list building blocks: `ListShell`, `ListPagination` + `useListPagination`, `StatusBadge`, `LoadingState`, `ErrorState`, and `TooltipHint` (use it instead of native `title`). `ResourceFrame` and `EmptyState` are exported from `pages/images.tsx` and imported by most pages — keep using them rather than duplicating.
- shadcn/ui conventions on Base UI primitives (official `base-nova` style, `baseColor: neutral`, Lucide icons; see `web/components.json`) provide all shared components; appearance comes from Tailwind CSS v4 semantic design tokens (`@theme inline` oklch light/dark layers in `web/src/styles.css`). Dark mode is the default. Keep styling token-driven (`bg-background`, `text-muted-foreground`, `--chart-*`) — no literal palette colors or one-off visual overrides. ECharts/xterm read `--chart-*`, `--background`, `--foreground` from `documentElement` so they follow the active theme.
- Dark-first with dark/light/system themes; Chinese/English localization is persistent; `Cmd/Ctrl+K` command palette is a primary navigation surface. Prefer dense lists, inline/context actions (icon-only row actions with tooltips, icon-plus-text toolbar actions), tabs, sheets, progressive disclosure — not dashboard card grids or rows of action buttons.

## Security invariants

Sessions are opaque, hashed server-side, in HttpOnly SameSite cookies; passwords are bcrypt; the first user becomes administrator. Secrets (Git/registry credentials, SSH keys, Docker TLS material, webhook secrets) are AES-GCM encrypted in SQLite under the key at `SUMA_SECRET_KEY_FILE` (mode `0600`) — losing it makes stored credentials unrecoverable. Credential material is passed to subprocesses through `0700` temp dirs / `0600` files removed on completion, failure, or cancellation. Never log passwords, tokens, secrets, or sensitive env values; mask likely secrets in API/UI output. Destructive actions require explicit confirmation and are audited — volume deletion and Project cleanup require typing the exact name. Validate identifiers and paths: Compose files stay under the Compose root, delivery files stay inside the detached worktree after symlink resolution. Clone URLs are HTTPS/SSH only, no embedded passwords. Plaintext Docker TCP is rejected unless its endpoint is loopback, a private-network IP, or a Tailscale IP, and the API requires typed endpoint confirmation before saving it.

## Working conventions

`PLANS.md` is the progress record — check an item only after its stated verification actually passes, and log out-of-scope ideas under Future rather than implementing them. Tests should exercise services and HTTP behavior without a live daemon; keep live-Docker coverage in the `dockersmoke`-tagged files. Prefer simple explicit composition over speculative interfaces or behavior-free repository layers.
