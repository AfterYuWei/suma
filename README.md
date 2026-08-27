# DockPort

DockPort is a focused, dark-first Web interface for managing multiple Docker Engines from one agentless control plane. It targets personal servers, HomeLabs, NAS devices, VPS hosts, and small teams that want fewer SSH and Docker CLI sessions without introducing an agent fleet or cluster orchestrator.

The MVP is a Go monolith that serves a React application, versioned REST APIs, and WebSockets. Runtime state always comes from the Docker Engine; SQLite stores only DockPort-owned users, sessions, settings, Compose/CD metadata, encrypted credentials, releases, tasks, and audit records.

## Features

- First-run administrator creation, bcrypt passwords, HttpOnly sessions, login, and logout
- Dense host overview and container list/detail with lifecycle actions
- Live Docker logs, xterm.js exec terminal with resize, and ECharts stats
- Image pull progress, image management, networks, and guarded volume management
- Compose workspace that distinguishes local and Git sources: local `compose.yml` and `.env` remain editable, while Git-sourced Compose files are read-only
- A separate Continuous Delivery workspace for any supported HTTPS/SSH Git remote
- Verified Git revisions, release history, approval or automatic delivery, authenticated webhooks, drift reporting, and guarded rollback
- Persistent task center with realtime logs, audit history, settings, themes, and `Cmd/Ctrl+K` command palette
- Persistent Chinese/English interface preference and custom accessible confirmation/input dialogs
- Node management for mounted Unix sockets and direct Docker TCP APIs, with mTLS credentials, connection status, and per-node bind-path allowlists
- A global node selector for Docker resources and Compose; CD can deploy one immutable Release to multiple target nodes in parallel with independent rollback

DockPort continuous delivery consumes an already deployable Compose declaration from Git. It deliberately does not run source builds, tests, image publishing, pipeline jobs, or repository-provided scripts. The Compose menu only identifies the source and presents Compose content; Git-delivered files are read-only there. Repository setup, synchronization, approvals, releases, deployment, drift, and rollback live in the separate Continuous Delivery menu. Git sources must pass DockPort's deployment-source policy before a release is created. See [CD-DESIGN.md](CD-DESIGN.md) for the repository, credential, webhook, release, and rollback model.

## Technology

The web application uses React 19, TypeScript, Vite, Tailwind CSS v4, Semi Design's React 19 package with the official Feishu Universe Design theme, TanStack Router and Query, Zustand, Lucide, Motion, Monaco, xterm.js, and ECharts. Universe Design owns component appearance; project CSS is limited to responsive structure and document-level accessibility rules. Run `npm run audit:universe` to enforce that boundary. The server uses Go, Gin, GORM, SQLite, the Docker Go SDK, Gorilla WebSocket, the Docker Compose CLI, and the Git CLI. OpenSSH is used for SSH Git remotes.

See [ARCHITECTURE.md](ARCHITECTURE.md), [API.md](API.md), [PLANS.md](PLANS.md), and [MVP-CHECKLIST.md](MVP-CHECKLIST.md).

## Development

Requirements: Node.js 22+, Go 1.26+, Docker Engine, and Docker Compose v2/v5. Git and an OpenSSH client are also required when exercising Git-backed delivery outside the DockPort container. The user running the server must have permission to open `/var/run/docker.sock`.

From the repository root, the quickest development workflow is:

```bash
make install
make dev
```

`make dev` starts the Vite frontend on `0.0.0.0:5173` and the Go backend on `:8081`. The separate development port lets it coexist with the production container, which uses port `8080` and may restart automatically after a reboot. Run the services separately with `make web-dev` and `make server-dev`. Use `make help` to list build, test, and Docker commands.

Development ports can be overridden when needed:

```bash
make dev DEV_HOST=0.0.0.0 DEV_WEB_PORT=3000 DEV_API_PORT=9080
```

The equivalent commands without Make are:

```bash
cd web
npm ci
npm run dev
```

In another terminal:

```bash
cd server
DOCKPORT_ADDRESS=:8081 go run -buildvcs=false ./cmd/dockport
```

Vite proxies `/api` and `/ws` to `127.0.0.1:8081` in development. Development data defaults to `server/data`. Override configuration with:

| Variable | Default |
| --- | --- |
| `DOCKPORT_ADDRESS` | `:8080` |
| `DOCKPORT_DATA_ROOT` | `./data` |
| `DOCKPORT_DATABASE` | `<data-root>/dockport.db` |
| `DOCKPORT_DOCKER_HOST` | `unix:///var/run/docker.sock` (first-run default-node bootstrap only) |
| `DOCKPORT_COMPOSE_ROOT` | `<data-root>/compose` |
| `DOCKPORT_BACKUP_ROOT` | `<data-root>/backups` |
| `DOCKPORT_COMPOSE_COMMAND` | `docker compose` |
| `DOCKPORT_GIT_COMMAND` | `git` |
| `DOCKPORT_GIT_ROOT` | `<data-root>/gitops` |
| `DOCKPORT_SECRET_KEY_FILE` | `<data-root>/secret.key` |
| `DOCKPORT_COOKIE_SECURE` | `false` |

`DOCKPORT_SECRET_KEY_FILE` protects Git and registry secrets, SSH keys, Docker client certificates/private keys, custom CA certificates, and webhook secrets stored in SQLite. The generated key file is required to decrypt those values: back it up securely with the database, restrict it to the DockPort process, and never commit or share it. Restoring the database without the matching key makes stored credentials unusable.

Quality gates:

```bash
cd web && npm run lint && npm run typecheck && npm run build
cd server && go test ./... && go build -buildvcs=false ./...
```

## Deployment

Build and start the monolith:

```bash
docker compose up -d --build
```

The container uses `restart: "no"`: it will not start automatically after a host reboot. Start it explicitly with `make docker-up` when production-container testing is needed. For normal local development, leave it stopped and use `make dev`.

Open `http://localhost:8080`, create the administrator, then sign in. The host directory `/opt/dockport/data` persists the SQLite database, local Compose projects, Delivery Project Git worktrees, and the local credential-encryption key. Set `DOCKPORT_DATA_PATH` to choose another absolute host path. DockPort mounts it at the same absolute location inside the container so relative bind mounts in local and delivery-sourced Compose files resolve correctly for the host Docker daemon. Back it up before destructive host maintenance.

The default deployment mounts `/var/run/docker.sock` because Docker management is effectively root-equivalent. Additional local sockets may be mounted into the monolith and registered as Unix nodes. Remote Docker APIs should use mutual TLS and a Docker TLS credential authorized only to that node. Plaintext TCP is rejected unless it is loopback. Restrict access to the DockPort HTTP endpoint, use HTTPS through a trusted reverse proxy for remote access, set `DOCKPORT_COOKIE_SECURE=true` behind HTTPS, and never expose the Docker daemon on unauthenticated TCP port 2375. Git webhooks are intentionally unauthenticated by the DockPort session cookie, so expose them only over HTTPS and configure a strong per-project webhook secret.

To stop DockPort without deleting data:

```bash
docker compose down
```

To view service output:

```bash
docker compose logs -f dockport
```

Example custom data location:

```bash
DOCKPORT_DATA_PATH=/srv/dockport docker compose up -d
```

## Current limitations

DockPort V2 remains one monolithic control-plane instance. It does not implement remote agents, SSH execution, high availability, distributed locks, cross-node transactions, Swarm, Kubernetes, LDAP/OIDC/SSO, source-build pipelines, full host monitoring, automatic backups, a marketplace, or a mobile application. A multi-node CD release is intentionally non-atomic: each node records its own outcome and only failed nodes are rolled back.
