# DockPort

DockPort is a focused, dark-first Web interface for managing one local Docker host. It targets personal servers, HomeLabs, NAS devices, VPS hosts, and small teams that want fewer SSH and Docker CLI sessions without introducing an agent fleet or cluster control plane.

The MVP is a Go monolith that serves a React application, versioned REST APIs, and WebSockets. Runtime state always comes from the Docker Engine; SQLite stores only DockPort users, sessions, settings, Compose project metadata, tasks, and audit records.

## Features

- First-run administrator creation, bcrypt passwords, HttpOnly sessions, login, and logout
- Dense host overview and container list/detail with lifecycle actions
- Live Docker logs, xterm.js exec terminal with resize, and ECharts stats
- Image pull progress, image management, networks, and guarded volume management
- Compose-first project editor for `compose.yml` and `.env`, validation, and lifecycle tasks
- Persistent task center with realtime logs, audit history, settings, themes, and `Cmd/Ctrl+K` command palette
- Persistent Chinese/English interface preference and custom accessible confirmation/input dialogs

## Technology

The web application uses React 19, TypeScript, Vite, Tailwind CSS v4, shadcn-style primitives, Base UI, TanStack Router and Query, Zustand, Lucide, Motion, Monaco, xterm.js, and ECharts. The server uses Go, Gin, GORM, SQLite, the Docker Go SDK, Gorilla WebSocket, and the Docker Compose CLI.

See [ARCHITECTURE.md](ARCHITECTURE.md), [API.md](API.md), [PLANS.md](PLANS.md), and [MVP-CHECKLIST.md](MVP-CHECKLIST.md).

## Development

Requirements: Node.js 22+, Go 1.26+, Docker Engine, and Docker Compose v2/v5. The user running the server must have permission to open `/var/run/docker.sock`.

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
| `DOCKPORT_DOCKER_HOST` | `unix:///var/run/docker.sock` |
| `DOCKPORT_COMPOSE_ROOT` | `<data-root>/compose` |
| `DOCKPORT_BACKUP_ROOT` | `<data-root>/backups` |
| `DOCKPORT_COMPOSE_COMMAND` | `docker compose` |
| `DOCKPORT_COOKIE_SECURE` | `false` |

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

Open `http://localhost:8080`, create the administrator, then sign in. The host directory `/opt/dockport/data` persists the SQLite database and Compose projects. Set `DOCKPORT_DATA_PATH` to choose another absolute host path. DockPort mounts it at the same absolute location inside the container so relative bind mounts in managed Compose files resolve correctly for the host Docker daemon. Back it up before destructive host maintenance.

The deployment mounts `/var/run/docker.sock` because Docker management is effectively root-equivalent. Restrict access to the DockPort HTTP endpoint, use HTTPS through a trusted reverse proxy for remote access, set `DOCKPORT_COOKIE_SECURE=true` behind HTTPS, and never expose the Docker daemon on unauthenticated TCP port 2375.

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

DockPort V1 is intentionally single-node and local-socket only. It does not implement remote agents, Swarm, Kubernetes, LDAP/OIDC/SSO, GitOps, Prometheus/Grafana, automatic backups, a marketplace, or a mobile application. These are V2 considerations, not hidden runtime dependencies.
