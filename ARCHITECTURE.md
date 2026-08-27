# SUMA Architecture

## System

SUMA is a single Go process serving the compiled React application, REST APIs, and WebSockets. Its node runtime registry connects to mounted Unix sockets or direct Docker TCP APIs and keeps only SUMA-owned records in SQLite.

```text
Browser -- HTTP / WebSocket --> Go monolith
                                 |-- Gin API and static web
                                 |-- authentication and sessions
                                 |-- domain services
                                 |-- CD/release reconciler
                                 |-- task and audit services
                                 |-- GORM --> SQLite
                                 `-- node registry --> Docker SDK / compose CLI --> unix:// socket(s)
                                                                   `-----------> tcp:// mTLS endpoint(s)
                                              `--> Git CLI --> configured Git remote
```

## Server boundaries

`server/internal/api` wires handlers and transport concerns. Feature services own business policy. `server/internal/docker` is the sole Docker SDK boundary. `server/internal/compose` is the sole Compose process boundary. `server/internal/git` is the sole Git process and Git-credential boundary. `server/internal/cd` owns synchronization, release records with fixed Git/config identity, deployment-source policy, deployment serialization, drift, webhook verification, and rollback policy. Handlers never import Docker SDK types and do not invoke Git or Compose commands directly.

Runtime Docker resources are not mirrored into SQLite. SQLite stores node definitions/status summaries, credential grants, node-scoped Compose metadata, global Delivery Projects and target snapshots, per-node deployments, tasks, and audit history. Each Docker Engine remains authoritative for its current resources. A runtime client is captured when work starts; updating or disabling a node prevents new work without invalidating the client already held by an in-flight task.

All asynchronous work is represented by a task with pending, running, success, failed, or canceled state. A bounded in-memory event broker streams fresh task output while SQLite retains task history. The same cancellation rule applies to container logs, stats, and terminal connections: closing the browser connection cancels the Docker operation and closes its stream.

## Compose and delivery domain boundaries

Compose and continuous delivery are separate aggregates with independent lifecycles:

- A Compose project owns editable `compose.yml` and `.env` files below the configured Compose root and exposes local orchestration actions.
- A Delivery Project owns repository configuration, synchronization policy, credentials, releases, approvals, deployment history, drift, and rollback state.
- Creating or deleting either project type never implicitly creates or deletes the other.
- Compose is one deployment adapter invoked by a delivery release; it is not the CD aggregate root.

Delivery uses a stable, project-owned Compose runtime namespace that is independent from both the repository worktree and local Compose project records. Git delivery is hosting-provider neutral. Repository identity is a validated HTTPS or SSH Clone URL, and authentication depends only on its transport. Webhook payload adapters are selected from signed request headers and are not persisted as repository configuration.

The CD path is intentionally one-way:

```text
manual sync, periodic reconciliation, or verified webhook
  -> Git fetch and exact commit resolution
  -> detached worktree cleanliness and path/source-policy checks
  -> docker compose config validation, runtime policy, and canonical config hash
  -> release record with exact commit/config identity
  -> approval/manual deploy or automatic delivery
  -> snapshot target nodes
  -> pull and deploy concurrently through a target-specific Compose runner
  -> per-node Docker health observation and result
  -> failed-node-only rollback when enabled
  -> aggregate release/task/audit final status
```

An in-process reservation plus project lock serializes sync, approval/rejection, delivery, and rollback for each Delivery Project, including work queued before its task goroutine starts. The background reconciler checks due projects at startup and every 15 seconds, using each project's configured synchronization interval. The Compose adapter always receives a stable explicit runtime name, project directory, ordered file list, and optional environment file, so different commit worktree paths do not change runtime identity. SUMA does not execute repository scripts, build application source, run tests, or build/publish images.

Approval and rejection are explicit release state transitions; approval alone does not deploy. Rollback creates and deploys a new release record based on a previously successful release. It never runs `down -v` and never reverses application data or database migrations. Manual rollback changes an `auto` project to `manual` before restoration. An automatic restoration after a failed Compose apply also changes the project to `manual`, preventing the polling reconciler from immediately replaying the newer revision. Git remains the desired source, so a long-term rollback should also revert or fix the repository revision.

## Web application

TanStack Router defines route ownership and TanStack Query caches server data. Every Docker-resource key includes `node_id`. Zustand persists the selected node and local display preferences. CD and Authentication Center queries remain global.

Semantic tokens (`background`, `surface`, `surface-hover`, `border`, `muted`, `text`, `text-muted`) support dark, light, and system themes. The shell uses a compact sidebar, contextual header, and global command palette rather than a conventional admin dashboard.

## Storage and paths

- Container deployment data root: fixed to `/Data` via `ENV SUMA_DATA_ROOT=/Data` in the Dockerfile; bare-metal runs default to `./data` and any deployment can override with the env var
- Default Compose root: `<data-root>/compose`
- Default Git root: `<data-root>/gitops`
- Default backup root: `<data-root>/backups`
- Default credential-encryption key: `<data-root>/secret.key`
- Bootstrap Docker endpoint: `unix:///var/run/docker.sock`; after first migration, nodes are database-owned

Local Compose project paths are resolved and checked beneath the configured Compose root. Delivery-project Compose files, configured or implicit environment files, referenced source files, and subdirectories are resolved after symlink evaluation and must remain inside the detached worktree; source files are regular files capped at 2 MiB. The Git client verifies both the recorded `HEAD` and a clean status, including untracked and ignored files, during synchronization and before deployment/rollback. It rejects a dirty worktree instead of mutating it. Deleting a Delivery Project removes that project's clone/worktrees through the Git adapter without changing local Compose projects. Deployments bind-mount the data directory at the same absolute host/container path so read-only repository bind sources resolve correctly, and mount the Docker socket without publishing Docker TCP ports.

## Security model

The first user becomes administrator. Passwords use bcrypt. Authentication uses opaque, hashed server-side sessions in HttpOnly cookies. State-changing user routes require an authenticated session. Destructive commands require explicit client confirmation and are audited server-side. Secrets are excluded from logs and sensitive environment keys are masked in API/UI representations.

The Authentication Center manages reusable Git, registry, and Docker TLS credentials with deny-by-default node allowlists. A multi-target CD project may use a credential only when every target is authorized. Registry and TLS material is passed through `0700` temporary directories and `0600` files, then removed on completion, failure, or cancellation. Sensitive material is encrypted with AES-GCM before SQLite persistence; the key file is created with mode `0600`.

Clone URLs are limited to HTTPS and SSH without embedded passwords. Local paths, `file://`, unauthenticated `git://`, and Git external transports are rejected. Recognized GitHub-compatible, GitLab-compatible, and generic webhook request formats share a size limit, per-project secret, repository/ref matching, and delivery-id deduplication. Webhooks enqueue a CD task and never trust their commit as deployment content—the server fetches and resolves the configured ref itself.

Git delivery applies an intentionally strict Compose policy. It rejects `include`, `extends`, `build`, missing images, privileged containers, host namespace sharing, devices, inherited container volumes, high-risk capabilities, disabled security confinement, Docker socket mounts, interpolated source paths, and writable or external bind mounts. File-backed configs/secrets plus `env_file` and `label_file` must also resolve to regular files inside the verified worktree. Named volumes remain available for persistent application data.

See [CD-DESIGN.md](CD-DESIGN.md) for the complete capability and operations contract.
