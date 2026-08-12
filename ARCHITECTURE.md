# DockPort Architecture

## System

DockPort is a single Go process serving the compiled React application, REST APIs, and WebSockets. It connects only to the local Docker Unix socket and keeps DockPort-owned records in SQLite.

```text
Browser -- HTTP / WebSocket --> Go monolith
                                 |-- Gin API and static web
                                 |-- authentication and sessions
                                 |-- domain services
                                 |-- task and audit services
                                 |-- GORM --> SQLite
                                 `-- adapters --> Docker SDK / compose CLI
                                                   |
                                          /var/run/docker.sock
```

## Server boundaries

`server/internal/api` wires handlers and transport concerns. Feature services own business policy. `server/internal/docker` is the sole Docker SDK boundary. `server/internal/compose` is the sole compose process boundary. Handlers never import Docker SDK types.

Runtime Docker resources are not mirrored into SQLite. The database contains users, sessions, settings, compose project metadata, tasks, audit logs, and login logs. Tables that may someday need it can carry `node_id = "local"`, without implementing node behavior.

All asynchronous work is represented by a task with pending, running, success, failed, or canceled state. A bounded in-memory event broker streams fresh task output while SQLite retains task history. The same cancellation rule applies to container logs, stats, and terminal connections: closing the browser connection cancels the Docker operation and closes its stream.

## Web application

TanStack Router defines route ownership and TanStack Query caches server data. Zustand stores local display preferences only. A typed API client normalizes the `{code,message,data}` envelope. Feature pages are composed from quiet primitives and dense Docker-specific rows.

Semantic tokens (`background`, `surface`, `surface-hover`, `border`, `muted`, `text`, `text-muted`) support dark, light, and system themes. The shell uses a compact sidebar, contextual header, and global command palette rather than a conventional admin dashboard.

## Storage and paths

- Container deployment data root: `/opt/dockport/data` by default, configurable with `DOCKPORT_DATA_PATH`
- Default Compose root: `<data-root>/compose`
- Default backup root: `<data-root>/backups`
- Docker endpoint: `unix:///var/run/docker.sock`

Compose project paths are resolved and checked beneath the configured Compose root. Deployments bind-mount the data directory at the same absolute host/container path so Compose bind sources resolve correctly, and mount the Docker socket without publishing Docker TCP ports.

## Security model

The first user becomes administrator. Passwords use bcrypt. Authentication uses opaque, hashed server-side sessions in HttpOnly cookies. State-changing routes require an authenticated session. Destructive commands require explicit client confirmation and are audited server-side. Secrets are excluded from logs and sensitive environment keys are masked in API/UI representations.
