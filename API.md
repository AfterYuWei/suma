# SUMA API

All REST responses use `{ "code": 0, "message": "success", "data": ... }`. Errors use a nonzero code and an appropriate HTTP status. Authentication uses the `suma_session` HttpOnly cookie. Its Secure flag is set automatically for direct TLS or a matching HTTPS browser origin; `SUMA_COOKIE_SECURE=true` can force it on.

Cookie-authenticated `POST`, `PUT`, `PATCH`, and `DELETE` requests and administrator initialization require an `Origin` matching the request host and port. When `security.browser_origin` is configured, the scheme must also match. JSON request bodies require `Content-Type: application/json`; avatar upload remains multipart, and signed Git webhooks use their own verification. Password login and administrator initialization return `429` with `Retry-After` when their short-term IP limits are reached. Browser WebSocket handshakes require the same allowed `Origin`; Agent WebSockets use dedicated bearer credentials and reject browser `Origin` headers.

## REST `/api/v1`

Node-aware clients use these routes:

- `GET|POST /nodes`, `GET|PUT|DELETE /nodes/:nodeID`, `POST /nodes/:nodeID/test`
- `GET|POST /node-groups`, `GET|PUT|DELETE /node-groups/:groupID`
- `GET /nodes/:nodeID/{overview,docker/info}`
- `GET|POST|PUT|PATCH|DELETE /nodes/:nodeID/{containers,images,networks,volumes,projects}/...`
- `POST /nodes/:nodeID/system/prune`
- `GET /nodes/:nodeID/tasks`, `GET /nodes/:nodeID/tasks/:taskID`, `GET /nodes/:nodeID/tasks/:taskID/{logs|steps}`, `POST /nodes/:nodeID/tasks/:taskID/cancel`
- `GET /nodes/:nodeID/audit-logs`

### Agent pairing and transport

When `SUMA_AGENT_PUBLIC_URL` is unset, Agent pairing uses the authenticated administrator request's same-host HTTPS `Origin`. The Agent host must be able to reach that address and trust its TLS certificate. `SUMA_AGENT_PUBLIC_URL` remains an explicit override for deployments where the Agent needs a different HTTPS origin. HTTP page origins cannot be used for automatic pairing. Agent connections require trusted TLS and do not use browser cookies.

- `POST /agent-enrollments` accepts `{ "name": "edge", "group_ids": [1] }` for a new node or `{ "node_id": "existing-node", "name": "ignored" }` for an in-place migration. It returns the node ID, a one-time 256-bit `token`, `expires_at`, and `public_url`; the token is never returned again.
- `GET /agent-enrollments/:nodeID` returns non-secret enrollment status; `POST /agent-enrollments/:nodeID/reissue` invalidates the old token and returns a new one; `DELETE /agent-enrollments/:nodeID` cancels pairing.
- `POST /nodes/:nodeID/agent/revoke` revokes the Agent credential and disconnects its streams. Re-pairing requires a new token.
- `POST /agents/enroll` accepts Agent protocol/version JSON and the one-time token in `Authorization: Bearer`; it exchanges the token once for a node-specific reconnect credential. `GET /ws/agents/control` and `GET /ws/agents/streams/:streamID` use that credential plus `X-SUMA-Agent-Node-ID`, `X-SUMA-Agent-Protocol`, and `X-SUMA-Agent-Version` headers. The control channel opens per-connection data streams; these routes are not browser APIs.

The Agent reads the initial token from `SUMA_AGENT_TOKEN` in its container environment. The generated Compose includes the token once; after pairing, the persisted identity allows the token variable to be removed.

Agent nodes expose `connection_type: "agent"`, server-managed `agent://<nodeID>` endpoints, `agent_version`, `agent_connected_at`, and optional `agent_enrollment` status. New nodes remain `pairing` until Docker Ping/Info and Engine ID uniqueness pass; an unsupported protocol is reported as `incompatible`. In-place migration changes the original node only after the Agent Engine ID matches; all existing node references remain stable. Agent Compose and takeover follow the same remote-source and bind-path rules as TCP nodes.

`GET|PUT /settings` retains optional advanced `security.browser_origin` and `security.trusted_proxies` strings; the settings page no longer asks for them. Empty browser origin uses exact host and port matching; a nonempty value must match the current page origin. Empty trusted proxies ignores forwarded client IP headers; otherwise use a comma-separated list of proxy IPs/CIDRs. Both values apply immediately after a successful `PUT`. The matching environment variables `SUMA_BROWSER_ORIGIN` and `SUMA_TRUSTED_PROXIES` supply initial values until a setting is saved. Trusted proxies cannot be inferred safely from request headers.

Node Groups are organizational filters, never Docker execution or authorization scopes. A node may belong to multiple Groups or have no Group. Node create/update accepts optional `group_ids`; omitting it on update preserves memberships, while an explicit empty array clears them. Deleting a Group only removes memberships. `GET /fleet/overview` accepts an optional `group_id=<id>` and probes only matching nodes; omitting it returns all nodes, including nodes without a Group.

`POST /nodes/:nodeID/containers/batch` accepts 1–100 IDs and a lifecycle `action`. Batch `start` and `stop` are idempotent: containers already in the requested state count as successful. Batch `restart` uses Docker's restart behavior for running and stopped containers. Batch `remove` may set `force: true` after destructive confirmation to remove running containers; `remove_volumes` remains independent and defaults to `false`. Every result includes `id`, `success`, and an optional Docker error for failed rows.

### Container files

All file routes require authentication and an explicit node and container ID. Their base is `/nodes/:nodeID/containers/:id/files`; no default-node alias exists. The container must be running and have `sh`, `stat`, `cat`, `sha256sum` for saves, and the standard file utilities used by the requested action. Operations run as the container's configured user through its Docker runtime, including TCP nodes; SUMA never opens bind source paths on its own host.

- `GET /files?path=/absolute/path&cursor=name` lists one directory page (200 entries), mount shortcuts, effective mount, and `next_cursor`. Entries identify regular files, directories, links, and special files. Mounts report `type`, `source`/`name`, container `destination`, `read_write`, and `is_directory`.
- `GET /files/content?path=/absolute/file` reads UTF-8 regular text up to 2 MiB and returns `content`, SHA-256 `etag`, `persistent`, `read_only`, and `single_file_bind`. Writable container layers, tmpfs mounts, bind mounts, and Docker volumes can be edited using the container's configured user. A read-only root filesystem or mount remains read-only; `persistent=false` identifies content that is lost when its container or tmpfs is removed.
- `PUT /files/content` accepts `{ "path": "/absolute/file", "content": "...", "etag": "<previous hash>" }`. A mismatched hash returns HTTP 409. Saves verify the resulting content. Single-file bind mounts use a guarded in-place write and have a brief non-atomic window.
- `POST /files/actions` accepts `{ "action": "create_file|create_directory|rename|copy|move|delete", "path": "/absolute/path", "target": "/absolute/destination" }`. `target` is required for rename/copy/move; rename stays in the same parent directory. Existing targets are never overwritten. Copy, move, and delete return a node-scoped Task to poll through `/nodes/:nodeID/tasks/:taskID`; other actions return the resulting paths.
- `GET /files/history?path=/absolute/file` lists encrypted editor revision metadata. `GET /files/history/:revision?path=...` returns one revision's content. `POST /files/restore` accepts `{ "path": "...", "revision_id": 123, "etag": "<current hash>" }` and creates a new revision on success.

Paths must be normalized container-absolute paths. Symlink traversal and editing nonregular files are rejected. Destructive operations require UI confirmation. The history database keeps at most 20 revisions per file for 30 days and 512 MiB total, with daily pruning; content is AES-GCM encrypted and excluded from audit records.

Compose Project names and managed directories must match Docker Compose's lowercase native `[a-z0-9][a-z0-9_-]*` identity. Mixed-case input is rejected instead of silently rewritten, and runtime ownership is matched by exact native name.

Compose requests containing a `/var/run/docker.sock` bind are rejected by default. After two explicit UI warnings, the confirmed request carries `X-SUMA-Allow-Docker-Socket: true`; this authorizes only that request. TCP and Agent node binds must still use non-interpolated absolute source paths.

The resource routes listed below remain deprecated aliases for the migrated default node. `GET /health` reports only control-plane/database health; a disconnected Docker node does not make it fail. Legacy `node_id` filters validate that the node exists. Global `GET /tasks` and `GET /audit-logs` accept `scope=control_plane|all`. Tasks default to `control_plane`; audit logs default to `all`, including ordinary and AI actions.

- `GET /health`, `GET /docker/info`
- `GET /auth/status`, `POST /auth/initialize`, `POST /auth/login`, `POST /auth/two-factor`, `POST /auth/passkey/options`, `POST /auth/passkey`, `POST /auth/logout`, `GET /auth/session`
- `PUT /account/profile`, `PUT /account/password`, `GET|PUT|DELETE /account/avatar`
- `GET|DELETE /account/two-factor`, `POST /account/two-factor/{setup|enable|recovery-codes}`
- `GET /account/passkeys`, `POST /account/passkeys/options`, `POST /account/passkeys`, `PATCH|DELETE /account/passkeys/:id`
- `GET /containers`, `GET /containers/:id`, `POST /containers/:id/{start|stop|restart|pause|unpause|kill}`, `PATCH /containers/:id`, `DELETE /containers/:id`
- `GET /images`, `GET /images/:id`, `POST /images/pull`, `POST /images/:id/tag`, `DELETE /images/:id`
- `GET|POST /networks`, `GET|DELETE /networks/:id`
- `GET|POST /volumes`, `GET|DELETE /volumes/:id`
- Deprecated default-node aliases remain under `/compose`; there is no legacy single-file copy operation.
- `GET /overview` for live host CPU, memory, disk, network, uptime, platform, and Docker summary data
- `GET /tasks`, `GET /tasks/:id`, `GET /tasks/:id/{logs|steps}`, `POST /tasks/:id/cancel`
- `GET /audit-logs`, `GET|PUT /settings`
- `POST /system/prune` starts a confirmed task for unused containers, networks, dangling images, and anonymous volumes

### Local account

SUMA currently has one local administrator and no role or permission model. First-run initialization requires `setup_token`, `username`, `email`, `password`, and `confirm_password`; `nickname` is optional. When no administrator exists, startup logs one random 256-bit `setup_token` in the `SUMA initialization key` JSON record; it expires after 30 minutes and rotates on restart. It remains only in process memory and is invalidated after successful initialization. The request body is limited to 16 KiB. Invalid keys return `403`, expired keys `410`, and an already initialized instance `409`. Login accepts `{ "username": "...", "password": "..." }`, where `username` may contain either the username or email address. A login without two-factor authentication returns `requires_two_factor: false` and the authenticated user. When TOTP is enabled it returns a five-minute `challenge_token` without creating a session; `POST /auth/two-factor` exchanges that challenge plus an authenticator or recovery code for the session cookie. Challenges permit at most five attempts, and repeated failures temporarily lock second-factor verification for the account.

`PUT /account/profile` accepts `username`, `nickname`, `email`, and `current_password`. The current password is required only when the username or email changes. `PUT /account/password` accepts `current_password`, `new_password`, and `confirm_password`; it preserves the requesting session and revokes the user's other sessions.

Avatar upload uses a multipart field named `avatar`. The web client accepts JPEG, PNG, or WebP sources up to 2 MB, crops them locally, and uploads a 512×512 WebP. The server validates the actual encoding and dimensions. `GET /account/avatar` is authenticated and returns the image body directly with an ETag rather than the JSON envelope.

TOTP enrollment starts with `POST /account/two-factor/setup` and the current password. The response contains a locally generated QR code, manual secret, and `otpauth://` URI and is marked `Cache-Control: no-store`. `POST /account/two-factor/enable` confirms a six-digit code and returns ten one-time recovery codes, which are never returned again. `POST /account/two-factor/recovery-codes` invalidates the old set and creates a new set after password and second-factor verification. `DELETE /account/two-factor` requires the same verification. Enabling, disabling, or regenerating recovery codes revokes every other session.

Passkeys use discoverable WebAuthn credentials with user verification required. `POST /auth/passkey/options` starts a passwordless login and `POST /auth/passkey` completes it. Registration starts at `POST /account/passkeys/options` with a display name, current password, and—when TOTP is enabled—a second-factor code; `POST /account/passkeys` completes the ceremony. Finish requests send the opaque one-time token returned by the options endpoint in `X-WebAuthn-Ceremony` and place the browser credential in the JSON body. Ceremonies expire after five minutes, are stored only as token hashes, and are bound to the exact HTTPS origin and RP ID (`http://localhost` is permitted for local development). Adding or deleting a Passkey revokes other sessions.

### Projects

SUMA uses `Project` as the first-level application/orchestration object. Current APIs implement `backend=compose`; the future `backend=swarm` model extension does not expose Swarm APIs.

- `GET|POST /nodes/:nodeID/projects`, `POST /nodes/:nodeID/projects/batch`
- `GET|PUT|DELETE /nodes/:nodeID/projects/compose/:name`
- `GET /nodes/:nodeID/projects/compose/:name/{services|logs}`
- `POST /nodes/:nodeID/projects/compose/:name/actions/:action`
- `POST /nodes/:nodeID/projects/compose/:name/takeover/{preview|render|validate}`
- `POST /nodes/:nodeID/projects/compose/:name/takeover`
- `POST /nodes/:nodeID/projects/compose/:name/takeover/shadow/assess`
- `POST /nodes/:nodeID/projects/compose/:name/takeover/shadow`
- `GET|DELETE /nodes/:nodeID/projects/compose/:name/takeover/shadow/:session`

`DELETE /nodes/:nodeID/projects/compose/:name` requires `confirm=<project-name>`. Adding `force=true` first runs `docker compose down --remove-orphans --timeout 0 --volumes`, then removes only the managed Project. Named volumes and their data are deleted by default; add `preserve_volumes=true` to omit `--volumes` and keep them. This operation never changes a Delivery Project.

`GET /nodes/:nodeID/projects` derives Compose Projects from Docker `com.docker.compose.*` labels instead of runtime rows in SQLite. Each response includes backend/scope identity, `source`, `managed`, capabilities, working-directory hints, and aggregated Service/Container Instance counts. SUMA-owned directories below the node Compose root keep a managed Project visible after `docker compose down` removes all labeled containers.

Project Takeover always operates on the entire Docker Compose Project. Preview aggregates every Service and Container Instance, detects scale/drift/one-off/orphan state, then either normalizes every safely accessible Local source file in label order or falls back for the whole Project to Inspect-based runtime reconstruction. Drafts remove normalized nulls, injected `com.docker.compose.*` labels, default port/bind/stop fields, service-name self aliases, implicit default-network declarations, and unused observed resources; simple long-form published ports are rendered in short form. Runtime reconstruction also subtracts image-owned command, entrypoint, user, working directory, environment, healthcheck, stop signal, exposed ports, and anonymous image volumes, plus engine-generated hostname/private IPC/default shared memory. Explicit custom values and named resources remain intact. TCP and Agent nodes never read remote label paths. Render applies per-variable `compose`/`.env`/exclude choices; validate checks the unsaved draft. Final takeover requires the exact native Project Name and current fingerprint, atomically writes managed files, and never calls pull/up/down.

The takeover body carries `mode`: `draft` (default) claims the reviewed generated draft, while `manual` claims a `compose.yml` and `.env` the operator wrote directly, skipping the generated draft and environment review. Both modes require the current preview fingerprint and pass the same content policy and `docker compose config` validation; `manual` is recorded as `takeover_source: manual` in the managed metadata and audited as `project.takeover_manual`. In both modes, and in `takeover/validate`, a declared top-level `name` or a `COMPOSE_PROJECT_NAME` entry in `.env` must equal the Project name, because the managed directory is deployed under that name.

`POST /nodes/:nodeID/projects/batch` accepts `{ "backend": "compose", "names": ["api", "worker"], "action": "restart" }` for 1–100 Projects. Supported actions are `start`, `stop`, `restart`, `update`, and `down`. Each valid Project starts an independent asynchronous task; one failure does not block the remaining Projects.

Shadow preview is optional and default-deny. Qualification rejects production-coupled ports, mounts, external networks, shared namespaces, privileged devices, build contexts, file dependencies, configs, and secrets. Eligible drafts run under a temporary `suma-preview-*` Compose Project. Accept/reject, TTL, page leave, and restart recovery clean it up; accepting still only saves the formal Project and does not switch traffic.

## Authentication center

These routes require a valid SUMA session:

- `GET|POST /credentials/git`
- `PUT|DELETE /credentials/git/:id`
- `GET|POST /credentials/registries`
- `PUT|DELETE /credentials/registries/:id`
- `GET|POST /credentials/docker-tls`
- `PUT|DELETE /credentials/docker-tls/:id`

Credential `auth_type` is one of:

| Type | Required input | Intended transport |
| --- | --- | --- |
| `none` | `name` | Public HTTPS repository |
| `http_token` | `name`, `secret`; optional `username` | Personal/project/deploy token over HTTPS |
| `http_basic` | `name`, `username`, `secret` | Username/password or username/token over HTTPS |
| `ssh_key` | `name`, `private_key`, `known_hosts`; optional `passphrase` | Pinned SSH transport |

`custom_ca` is optional for any HTTPS Git remote using a private CA. Do not disable TLS verification. Credential secrets, private keys, passphrases, `known_hosts`, and CA contents are encrypted at rest and are never returned by the API. A `PUT` request may omit an existing sensitive value to preserve it.

Git, registry, and Docker TLS credential requests include `authorized_node_ids`; the default is empty. Registry credentials use `basic` (username and password) or `token` authentication and are passed through a temporary Docker config only when explicitly selected. Docker TLS credentials contain a CA, client certificate, and private key and are never returned after creation.

## Continuous delivery projects

The CD API manages independent Delivery Projects. A project may deploy Compose declarations and prebuilt images through the Compose adapter, but it is not a Compose project and has an independent lifecycle. It does not build source, run tests, publish images, or execute pipeline jobs.

Authenticated routes:

- `GET|POST /delivery-projects` lists or creates Delivery Projects; creation accepts `node_ids`.
- `GET|DELETE /delivery-projects/:name` reads or deletes one project. Deletion requires `confirm=<project-name>`; optional `force=true` tears down its active runtime.
- `GET|PUT /delivery-projects/:name/configuration` reads or updates repository and delivery configuration.
- `POST /delivery-projects/:name/sync` queues a manual Git synchronization and returns `202 Accepted` with a task.
- `GET /delivery-projects/:name/drift` compares the desired Git commit with the active release.
- `GET /delivery-projects/:name/releases` lists up to 100 releases, newest first.
- `GET /delivery-projects/:name/releases/:releaseID` reads one release.
- `POST /delivery-projects/:name/releases/:releaseID/{approve|reject|deploy|rollback}` performs the corresponding release transition.
- `POST /delivery-projects/:name/releases/:releaseID/remediations/retry-failed` retries only server-selected failed, interrupted, or auto-rolled-back target snapshots.
- `POST /delivery-projects/:name/releases/:releaseID/remediations/rollback-failed` restores each eligible failed target to its own `previous_release_id`; it does not create a release.

A representative `PUT /delivery-projects/:name/configuration` body is:

```json
{
  "repository": {
    "clone_url": "ssh://git@git.example.internal:2222/platform/app-deploy.git",
    "ref_type": "branch",
    "ref": "main",
    "authentication": {
      "source": "center",
      "credential_id": 4
    },
    "compose_files": ["compose/base.yml", "environments/production.yml"],
    "environment_file": "env/production.env"
  },
  "reconcile_mode": "manual",
  "sync_interval_seconds": 300,
  "auto_rollback": false,
  "deployment_timeout": 120,
  "webhook_enabled": true,
  "webhook_secret": "replace-with-a-long-random-secret"
}
```

Repository authentication is explicit: `none` uses no credential, `center` references a reusable Git credential, and `project` uses an encrypted credential owned only by the current Delivery Project. A new project credential may include `save_to_center: true`; the configuration transaction creates the reusable credential, links it to the project, and removes the project-only copy. Project credential secrets are never returned by GET.

Repository constraints:

- There is no hosting-provider field. `clone_url` is the only repository endpoint and may target any standards-compatible HTTPS or SSH Git server.
- HTTPS, `ssh://`, and SCP-style `user@host:path` clone URLs are supported. Local paths, embedded credentials, insecure `git://`, external remote helpers, queries, fragments, and traversal are rejected.
- `ref_type` is `branch`, `tag`, or `commit`; a commit ref must be a full SHA.
- Compose-file and environment-file values are relative to the repository root. Compose files may live in different directories. Absolute paths, traversal, invalid segments, and symlink escape are rejected.
- At least one ordered Compose file is required.
- Compose files are passed to Docker Compose in list order. The first file establishes the project directory for relative Compose paths; later files override or extend earlier files according to Docker Compose merge rules.
- `reconcile_mode` is `observe`, `manual`, or `auto`. Observe synchronizes and reports but blocks approval, deployment, and rollback. Manual prepares a release for review. Auto synchronizes and delivers a valid release.
- `sync_interval_seconds` accepts 30 through 86400 seconds. The background reconciler scans for due Git projects at startup and every 15 seconds; manual and verified-webhook triggers are also available.
- `deployment_timeout` accepts 10 through 3600 seconds and is passed to Compose health waiting.

Before creating a release, SUMA enforces the deployment-source policy described below. Before every deployment or rollback it also verifies that the detached worktree is still at the recorded commit and contains no tracked modifications, untracked files, or ignored generated files. A dirty worktree fails the operation; SUMA does not reset or clean it automatically. Local `/compose` APIs never expose or mutate Delivery Projects.

### Git deployment-source policy

For Git delivery, every configured Compose/environment file, implicit project `.env`, and referenced local source must resolve inside the Git worktree, be a regular file, and be no larger than 2 MiB. The policy also applies to Compose `env_file`, `label_file`, and file-backed top-level `configs` and `secrets`.

SUMA rejects:

- Compose `include` and service `extends`;
- every service `build` declaration or service without a prebuilt `image`;
- source-file paths containing any `$` interpolation form;
- `privileged`, host network/PID/IPC/cgroup namespaces, host devices, inherited container volumes, high-risk capabilities, and disabled security confinement;
- a Docker socket mount;
- any bind mount that is writable or resolves outside the Git worktree.

Named volumes remain supported. Repository bind sources must already exist and be read-only. This is a deliberately strict single-host delivery policy, not a general-purpose Compose compatibility promise.

When SUMA generates a webhook ID or secret, `GET` never reveals the stored secret. Treat a secret returned by the configuration update as one-time material.

## Git webhooks

The webhook route is deliberately outside session-cookie authentication:

```text
POST /api/v1/webhooks/git/:hookID
```

It accepts at most 2 MiB, verifies the per-project secret before enqueueing work, checks the repository and configured branch/tag, deduplicates delivery IDs, then returns `202 Accepted`. The payload does not become deployment input; SUMA fetches the configured remote and resolves the exact commit itself.

SUMA selects a compatible payload adapter from standard request headers; this is not persisted as repository-provider configuration.

### GitHub-compatible request

- Configure a Push event webhook.
- Sign the raw body with the project secret and send `X-Hub-Signature-256: sha256=<hex>`.
- SUMA uses `X-GitHub-Delivery` for idempotency and requires `X-GitHub-Event: push`.

### GitLab-compatible request

- Configure Push Hook and, when tracking tags, Tag Push Hook.
- Configure GitLab's secret token; SUMA verifies the standard `X-Gitlab-Token` using constant-time comparison. An ingress or compatible sender may instead provide `Webhook-Signature: sha256=<hex>` with a fresh `Webhook-Timestamp`.
- `X-Gitlab-Webhook-UUID` or `X-Gitlab-Event-UUID` is used for idempotency when present.

The webhook URL points to SUMA, not the configured GitLab base URL, so a self-managed GitLab must have network access to that HTTPS endpoint.

### Generic request

- Send `Authorization: Bearer <webhook-secret>`.
- Send `Idempotency-Key` when possible.
- The JSON body supplies repository identity and an optional ref only to select/validate the trigger:

```json
{
  "repository": "https://git.example.net/team/app-deploy.git",
  "ref": "refs/heads/main"
}
```

SUMA still fetches its stored clone URL; the request cannot choose a Compose file, command, image, or commit to execute.

## Approval, delivery, and rollback behavior

Only one synchronization, approval/rejection, deployment, or rollback may be queued for a project at a time. `approve` and `reject` complete synchronously. Deployment and rollback return tasks whose final Task and Audit result reflects the asynchronous outcome.

Deployment pulls declared images, runs `docker compose up -d --remove-orphans --wait`, then requires every reported service to be running and, when a health status exists, healthy. Each node operation creates an immutable Deployment Attempt (`deploy`, `retry`, `manual_rollback`, or `auto_rollback`) linked to its child Task. The Deployment row remains the latest per-release/node summary.

Drift probes every current target through its own Compose target with a five-second node timeout, concurrency capped at eight, and a five-second shared cache. `status` is `healthy`, `degraded`, or `unknown`; an unreachable node is unknown without being falsely marked drifted, while any confirmed commit/runtime failure makes the aggregate degraded. `nodes` carries the active release/commit, reason code, health summary, and check time for each target. Project-level active release/commit is present only when all current targets agree.

On startup, SUMA atomically marks nonterminal Attempts `interrupted`, their Deployment summaries failed, and pending/running parent and child Tasks canceled, then recomputes Release and project aggregates. Recovery never connects to Docker, retries, or rolls back automatically.

Rollback creates a new release record from a previously `succeeded` or `rolled_back` release; it does not rewrite the old record. If the project was in `auto` mode, a manual rollback first changes it to `manual` so polling cannot immediately redeploy the newer Git revision. A failed `docker compose up` may trigger automatic restoration when `auto_rollback` is enabled; that path also switches reconciliation to `manual`. Re-enable `auto` explicitly only after Git and the desired runtime state have been reconciled.

## WebSocket

- `/ws/containers/:id/logs` streams timestamped UTF-8 log chunks.
- `/ws/containers/:id/stats` streams Docker Stats JSON samples.
- `/ws/containers/:id/terminal` streams binary terminal output and accepts binary input or JSON `{type:"input",data}` / `{type:"resize",cols,rows}`.
- `/ws/tasks/:id` replays retained task logs and streams progress/status events, including CD sync, delivery, and rollback tasks.
- `/ws/nodes/:nodeID/tasks/:taskID` is the canonical node-task stream. The node and exact task scope/ownership are checked before WebSocket upgrade.

All WebSockets require the same session cookie as REST. Disconnecting cancels the underlying context and closes Docker streams or exec sessions.

Node-aware realtime routes are `/ws/nodes/:nodeID/containers/:id/{logs,stats,terminal}`. Legacy container WebSockets are default-node aliases.

## Scheduled Docker storage cleanup

All cleanup endpoints require the existing authenticated session and request-origin protections. The node must exist; previews and run IDs are bound to that node.

| Method and path | Behavior |
| --- | --- |
| `GET /api/v1/cleanup/policies` | Node policy summaries, next occurrence, latest and active run |
| `GET /api/v1/nodes/:nodeID/cleanup/policy` | Policy, live Engine capabilities and next three occurrences |
| `PUT /api/v1/nodes/:nodeID/cleanup/policy` | Replace configuration with optimistic `version` checking |
| `POST /api/v1/nodes/:nodeID/cleanup/preview` | Send `{}`; read-only five-minute preview of the saved policy |
| `POST /api/v1/nodes/:nodeID/cleanup/run` | Send `preview_id` and exact `confirmation_name`; returns a node Task with HTTP 202 |
| `GET /api/v1/nodes/:nodeID/cleanup/runs?page=1&failed=false` | Twenty execution records per page, policy snapshots and partial results |
| `GET /api/v1/nodes/:nodeID/cleanup/runs/:runID` | One execution with Task ID and per-category results |

A policy update sends the complete configuration below. Initial `version` is zero. Enabling scheduling, reducing retention or budget, removing protections, or adding automatic deletion categories requires `authorize: true` and the exact node name in `confirmation_name`. Copying configuration in the UI leaves scheduling disabled. There is no automatic-volume-deletion field; unknown JSON fields are rejected.

```json
{
  "version": 0,
  "enabled": false,
  "schedule": { "frequency": "weekly", "weekday": 0, "hour": 3, "minute": 0, "timezone": "UTC" },
  "images": { "enabled": true, "retention_days": 7, "include_tagged": false },
  "cache": { "enabled": true, "retention_days": 7, "reserved_bytes": 10737418240 },
  "containers": { "enabled": false, "retention_days": 7 },
  "networks": { "enabled": false, "retention_days": 7 },
  "scan_volumes": true,
  "protected": { "image": [], "container": [], "network": [], "volume": [] }
}
```

`weekday` uses Sunday=0. Timezones use embedded IANA data. Missing daylight-saving times are skipped; repeated times run once. Retention is 1–3650 days. The cache budget is a best-effort Engine parameter, not an exact disk-space guarantee. Negotiated API 1.39–1.47 uses `keep-storage`; API 1.48+ uses `reserved-space`; earlier APIs skip cache cleanup explicitly. Cache pruning always uses `all=false` and an `until` duration based on last use. See the [Engine API version history](https://docs.docker.com/reference/api/engine/version-history/) for the parameter rename.

Stale policy versions, expired or changed previews, changed node runtimes and concurrent node executions return 409. Confirmations and invalid policies return 422. Unavailable nodes return 503. Resource failures continue within the frozen candidate set, producing `partial_failed` when appropriate; disconnects, cancellation or lost protection information stop further deletion. The associated Task reports failure for partial failures. Task cancellation uses the existing node Task endpoint.

Volumes never enter automatic deletion. `DELETE /api/v1/nodes/:nodeID/volumes/:name?confirm=:name` requires the exact volume name and performs fresh reference/protection checks followed by non-force Engine removal. Previews estimate individual sizes without summing shared image layers. Reports distinguish Engine cache reclaimed bytes from before/after image-layer, container-writable-layer and volume usage observations. Unknown usage remains null. Mutable historical image tags protect only images currently resolving to those references; they do not guarantee offline rollback after a tag has been overwritten.

Both legacy `/system/prune` routes still accept `{"confirm":"PRUNE"}` and generate a fresh preview through this same service, using the saved node policy. They no longer invoke blanket Engine prune or delete volumes.

Repeatable isolated Unix/mTLS/Agent verification is documented in [Docker cleanup smoke verification](doc/cleanup-smoke.md).

## Notifications and reviewed AI operations

All routes require an authenticated session and the usual Origin check for writes. Secrets are write-only; blank fields retain encrypted material. Channel/rule/settings writes carry an optimistic `version`. See [setup, provider permissions and approval behavior](doc/notifications-and-ai-operations.md).

| Method | `/api/v1` path | Behavior |
| --- | --- | --- |
| GET | `/notifications/catalog` | Canonical events and rule presets |
| GET / POST | `/notifications/channels` | List or create a channel |
| PUT / DELETE | `/notifications/channels/:id` | Edit/pause or delete; identity changes revoke bindings |
| POST | `/notifications/channels/:id/check` | Check bot credentials (does not prove delivery/callback setup) |
| POST | `/notifications/channels/:id/test` | Send an explicit test message, including while paused |
| GET | `/notifications/channels/:id/chats` | Detected chats from incoming platform events |
| GET / POST | `/notifications/rules` | List or create a routing rule |
| PUT / DELETE | `/notifications/rules/:id` | Edit or remove a rule |
| GET | `/notifications/inbox` | Latest 200 historical messages plus total per-user unread count |
| POST | `/notifications/inbox/:id/read` | Persist per-user read state |
| GET | `/notifications/deliveries` | Latest 200 delivery attempts and suppression reasons |
| POST | `/notifications/deliveries/:id/resend` | Create a new durable delivery |
| GET / POST | `/notification-bindings` | List own bindings or issue a 10-minute private-chat code |
| POST | `/notification-bindings/:id/confirm` | Confirm a claimed stable platform identity |
| DELETE | `/notification-bindings/:id` | Revoke own binding and associated approval tokens |
| GET / PUT | `/ai/settings` | Read/update enablement, model, node scope and limits; API key is write-only |
| POST | `/ai/settings/test` | Test text and registered function calling separately |
| GET / POST | `/ai/runs` | History or start an authorized-node diagnosis (202) |
| GET | `/ai/runs/:id` | Status, redacted evidence, summary and proposal IDs |
| GET | `/ai/operations` | Individually reviewed proposals and execution history |
| GET | `/ai/operations/:id` | Full frozen preview, expiry, `review_token`, status and linked Task |
| POST | `/ai/operations/:id/decision` | `{ "approve": true, "review_token": "..." }`; no parameter overrides |
| GET | `/ai/audit` | Bounded diagnosis/approval/execution audit history |

`GET /ws/ai/runs/:id` streams diagnosis snapshots until completion and cancels its polling context on disconnect. Execution progress uses existing node Task APIs/WebSockets. Creating a proposal never executes; a successful decision atomically creates the pending Task, claims approval and records reviewer identity, then launches work after commit. Expired/changed/repeated approvals return 409, disabled/out-of-scope access 403 and exhausted concurrency/automatic budget 429. Restart recovery does not replay AI mutations.

`GET /audit-logs` is the unified global audit source, including `ai.*` actions. Records may include `source`, `run_id`, `operation_id`, `task_id`, platform identity and redacted `details`. `GET /ai/audit` returns only the AI projection of these same records and preserves their IDs and correlations. Both require a SUMA session; chat guest queries cannot read either endpoint.
