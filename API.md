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

### Standalone container creation

`POST /nodes/:nodeID/containers` accepts `name`, `image`, `pull_policy` (`missing`, `always`, `never`), optional `credential_id`, `start` (defaults to `true`), `network` (defaults to `bridge`), `restart_policy` (`no`, `unless-stopped`, `always`, `on-failure`), `ports`, `mounts`, `environment`, `command`, `entrypoint`, `working_directory`, `user`, `memory_mb`, `cpus`, `read_only_rootfs`, `tty`, and `open_stdin`. It returns HTTP `202` with a node-scoped `container.create` Task. Registry credentials must be authorized for the selected node and match the image's registry.

- Ports use `{ "container_port": 80, "host_port": 8080, "host_ip": "127.0.0.1", "protocol": "tcp" }`. Host port `0` requests an available port; the default host IP is loopback. TCP and UDP are supported. `host` and `none` networks cannot publish ports.
- Mounts use `{ "type": "volume", "source": "app-data", "target": "/data", "read_only": false }` or `type: "bind"` with a non-interpolated absolute path on the Docker node. Targets must be distinct absolute paths below root. Docker socket sources/targets at `/var/run/docker.sock` or `/run/docker.sock` require `X-SUMA-Allow-Docker-Socket: true`, including read-only mounts.
- Environment entries use `{ "key": "APP_ENV", "value": "production" }`; duplicate keys are rejected. Command and entrypoint are argument arrays, with no host shell evaluation. Empty arrays inherit image defaults. `memory_mb` and `cpus` use `0` for no limit; a nonzero memory limit must be at least 6 MiB.

Progress streams through `/ws/nodes/:nodeID/tasks/:taskID`; polling, logs, steps, and cancellation use the existing node Task endpoints. A `container:<Docker ID>` step records creation before startup. Startup failure or cancellation after creation retains that container for inspection; cancellation does not delete resources. Task/audit evidence excludes environment values and registry secrets. Configuration is not persisted for automatic replay; interrupted tasks follow the existing restart recovery policy. Closing a progress stream releases its subscription while the task continues.

### Agent pairing and transport

When `SUMA_AGENT_PUBLIC_URL` is unset, Agent pairing uses the authenticated administrator request's same-host HTTPS `Origin`. The Agent host must be able to reach that address and trust its TLS certificate. `SUMA_AGENT_PUBLIC_URL` remains an explicit override for deployments where the Agent needs a different HTTPS origin. HTTP page origins cannot be used for automatic pairing. Agent connections require trusted TLS and do not use browser cookies.

- `POST /agent-enrollments` accepts `{ "name": "edge", "group_ids": [1] }` for a new node or `{ "node_id": "existing-node", "name": "ignored" }` for an in-place migration. It returns the node ID, a one-time 256-bit `token`, `expires_at`, and `public_url`; the token is never returned again.
- `GET /agent-enrollments/:nodeID` returns non-secret enrollment status; `POST /agent-enrollments/:nodeID/reissue` invalidates the old token, revokes the old credential, disconnects the Agent, and returns a new token; `DELETE /agent-enrollments/:nodeID` cancels pairing.
- `POST /nodes/:nodeID/agent/revoke` revokes the Agent credential and disconnects its streams. Re-pairing requires a new token.
- `POST /agents/enroll` accepts Agent protocol/version JSON and the one-time token in `Authorization: Bearer`; it exchanges a token valid for at most ten minutes once for a node-specific persistent reconnect credential. The credential has no expiry and remains valid until explicitly revoked, replaced by manual token refresh/new pairing, or removed when the node is deleted or switches to Unix/TCP. `GET /ws/agents/control` and `GET /ws/agents/streams/:streamID` use that credential plus `X-SUMA-Agent-Node-ID`, `X-SUMA-Agent-Protocol`, and `X-SUMA-Agent-Version` headers. The control channel opens per-connection data streams; these routes are not browser APIs.

The Agent reads the initial token from `SUMA_AGENT_TOKEN` in its container environment. The generated Compose includes the token once; after pairing, the persisted identity allows the token variable to be removed.

Agent nodes expose `connection_type: "agent"`, server-managed `agent://<nodeID>` endpoints, `agent_version`, `agent_connected_at`, and optional `agent_enrollment` status. New nodes remain `pairing` until Docker Ping/Info and Engine ID uniqueness pass; an unsupported protocol is reported as `incompatible`. In-place migration changes the original node only after the Agent Engine ID matches; all existing node references remain stable. Agent Compose and takeover follow the same remote-source and bind-path rules as TCP nodes.

`GET|PUT /settings` retains optional advanced `security.browser_origin` and `security.trusted_proxies` strings; the settings page no longer asks for them. Empty browser origin uses exact host and port matching; a nonempty value must match the current page origin. Empty trusted proxies ignores forwarded client IP headers; otherwise use a comma-separated list of proxy IPs/CIDRs. Both values apply immediately after a successful `PUT`. The matching environment variables `SUMA_BROWSER_ORIGIN` and `SUMA_TRUSTED_PROXIES` supply initial values until a setting is saved. Trusted proxies cannot be inferred safely from request headers.

Node Groups are organizational filters, never Docker execution or authorization scopes. A node may belong to multiple Groups or have no Group. Node create/update accepts optional `group_ids`; omitting it on update preserves memberships, while an explicit empty array clears them. Deleting a Group only removes memberships. `GET /fleet/overview` accepts an optional `group_id=<id>` and probes only matching nodes; omitting it returns all nodes, including nodes without a Group.

The home page first renders metadata from `GET /nodes`, then loads each enabled node independently through authenticated `GET /nodes/:nodeID/overview/{info|metrics|resources|disk}`. Each read has a five-second deadline including runtime resolution; disabled/offline nodes do not hold up other cards. `info` returns Engine information, `resources` returns `{networks, volumes}`, and `disk` returns `{docker_disk_usage_bytes}`. `metrics?size=small|medium|large` defaults to `medium`: small returns only `metrics_available`, aggregate CPU and memory, without container inspection or rows; medium adds aggregate network/block counters, longest uptime and CPU/memory container rows; large also includes each container's network/block counters and uptime. Invalid sizes return 400 before accessing Docker. A failed container sample is marked `available=false`, making aggregate `metrics_available=false`. Nodes, information, metrics and resource counts refresh every ten seconds; disk usage refreshes every sixty seconds. Resizing/filtering/navigation abort unobserved reads; failed sections can be retried independently. The existing full `GET /fleet/overview` remains available.

`POST /nodes/:nodeID/containers/batch` accepts 1–100 IDs and a lifecycle `action`. Batch `start` and `stop` are idempotent: containers already in the requested state count as successful. Batch `restart` uses Docker's restart behavior for running and stopped containers. Batch `remove` may set `force: true` after destructive confirmation to remove running containers; `remove_volumes` remains independent and defaults to `false`. Every result includes `id`, `success`, and an optional Docker error for failed rows.

### Container runtime

`GET /nodes/:nodeID/containers/runtime` reads health status, restart count and the configured memory limit for all containers, including stopped containers. Each row contains `id`, `health_status`, `restart_count` and `memory_limit_bytes`; unavailable fields are `null`. Health status `none` means no healthcheck, `unknown` means its status could not be established, and a memory limit of `0` means unlimited. The authenticated read has a five-second deadline including node resolution and at most six concurrent Engine inspections. Individual inspection failures retain the row with unknown fields; a failed list or request deadline returns 503. This endpoint only reads Docker and does not persist runtime state.

Container metrics also return `available`, network receive/transmit byte counters, and optional `sampled_at` (Docker Stats read time) and `started_at` (last start time). The container list loads its basic rows immediately, refreshes metadata every ten seconds and metrics every five seconds. Network rates are byte differences divided by the actual interval between valid samples; the first sample, restart, counter rollback, failed read or interval longer than fifteen seconds resets the baseline. Missing sample/start timestamps make the rate unavailable. Stopped containers show no live resource or network values. Full port mappings preserve all binding IPs even when their compact summaries are grouped.

### Container diagnostics, topology and events

`GET /nodes/:nodeID/containers` includes `networks`, an alphabetically sorted array of `{name, ip_address?, ipv6_address?}` taken directly from the Engine ContainerList response. Missing network settings return `null`; known no attachments return `[]`. No per-container Inspect or network-list request is added. The list has no expansion or quick-view endpoint. Its Network column shows the first attachment's name and IPv4 (IPv6 when IPv4 is absent); a `+N` indicator and shared tooltip expose all attachments without querying Docker. Unknown or unassigned IPs show a dash. Receive/transmit rates are in the separate Traffic column. Port tags use `host:container` for TCP, retaining a protocol suffix for other protocols; exposed-only ports show only the container port. Full port mappings retain every binding IP and protocol in the popover. Detailed topology remains in `GET /nodes/:nodeID/containers/:id`. Resource trend actions and the resource column now open the same persisted monitoring view for the last hour. They query PostgreSQL monitoring history without opening per-row Docker stats streams. Live list metrics reuse fresh collector samples where available.

`GET /nodes/:nodeID/containers/runtime` additionally returns nullable `state`, `started_at`, `finished_at`, `exit_code`, `oom_killed`, `state_error` and `observed_at`. Zero Docker timestamps become null; an exit code is only returned for a finished, non-running container. Restart history alone does not establish a restart loop, and exit code 137 alone does not establish OOM.

`GET /nodes/:nodeID/containers/:id/runtime` returns `{runtime, restart_policy, health}`. Health includes nullable configuration/intervals in milliseconds, failing streak, last check time, a redacted command and at most five retained check records. Records contain start/end timestamps, exit code, bounded UTF-8 output and redaction/truncation flags. Known sensitive environment values and generic credentials are redacted before egress; uncertain short sensitive values hide the diagnostic. The endpoint is `Cache-Control: no-store` and has a five-second deadline including node resolution.

Container detail mounts additionally return nullable `anonymous`, derived from explicit Engine mount/bind configuration or image-declared anonymous volumes. Inherited or conflicting ownership stays unknown; a generated-looking name is not used as evidence. Container detail also returns `network_mode` and per-network `ipv6_address` and `ipv6_gateway`. Bind sources are informational and are never read on the control-plane host through these observation endpoints.

`GET /nodes/:nodeID/containers/:id/events?since=<RFC3339>&until=<RFC3339>&limit=200` requires a full 64-character hexadecimal container ID. The default range is one hour, maximum range is 24 hours, and limit is 1–256. Its envelope data contains `events`, `source="docker"`, `coverage="engine_retained"`, requested range and `truncated`. Truncation only describes SUMA's result limit; Docker retains at most 256 recent Engine events, so completeness is not promised. Historical queries do not require the container to still exist. No events are newly persisted by this API.

`/ws/nodes/:nodeID/containers/:id/events?since=<RFC3339>` streams `{type:"events",entries:[...]}` or `{type:"error",code,message}`. Event fields include stable ID, node/container identity, RFC3339Nano time, decimal-string `time_nano`, action and optional signal/health status/exit code. Only lifecycle, health and OOM actions and whitelisted attributes are exposed. Browser session and Origin checks apply. The stream has bounded buffering, heartbeat/read deadlines and cancels upstream Docker/Agent requests on disconnect.

`/ws/nodes/:nodeID/containers/:id/stats?format=normalized` emits normalized `ContainerMetrics` JSON samples; omitting format preserves the existing raw Docker format. Metrics add `cpu_available` and `memory_available` so clients distinguish missing fields from zero. Linux memory subtracts `total_inactive_file` on cgroup v1 or `inactive_file` on v2, consistently across list and live statistics. The normalized stream remains compatible. The Monitoring tab uses persisted monitoring queries; the old #stats detail hash redirects to #monitoring. UI preferences alone are persisted in localStorage.

### Container monitoring history

All `/monitoring` routes require a session; mutations also require the normal same-origin checks. Historical reads never resolve a live Docker runtime, so retained evidence remains available for offline or deleted nodes and containers.

- `GET /monitoring/settings` returns `{enabled,interval_seconds,retention_days,exclusions,revision}`. Defaults are enabled, 5 seconds and 3 days. Intervals are 5/10/15/30/60 seconds; retention choices are 1/3/7/14/30 days. Up to 200 exclusions match a node, full container ID, or a node-local Compose project and optional service. All matching rules exclude collection.
- `PUT /monitoring/settings` accepts those fields and `confirm_retention_reduction`. The supplied revision must match the stored revision. Reducing retention requires explicit confirmation; stale settings and absent confirmation return 409. The response is `{settings,task_id}`; a durable pending-cleanup checkpoint recovers interrupted cleanup through the Task service. Changes are audited. Disabling collection preserves existing history until expiry.
- `GET /monitoring/status` returns the effective policy, per-node stream count/last successful write/safe failure reason, active collection count, estimated daily records and the latest cleanup Task ID.
- `GET /monitoring/instances?search=&node_id=&container_id=&since=&until=&page=1&limit=50` returns `{items,total,page,nodes}`. The maximum page size is 100. `nodes` includes all retained historical nodes independently of pagination and filters. Instance identity is the stable hash of node ID, Engine ID and full container ID; names/Compose attribution are historical context. Restart keeps the instance; rebuilding or changing Engine creates a different instance.
- `GET /monitoring/instances/:instanceID` reads one retained historical instance. Expired/unknown instances return 404.
- `POST /monitoring/query` accepts `{targets:[{node_id,engine_id,container_id,since,until}],max_points:1440}`. One to four targets are allowed; each range is positive and at most 30 days, using RFC3339 timestamps. Different equal-length ranges support period comparison. The response includes `series` and `retained_since`; each series includes the instance, original target, actual `step_seconds`, points, summary, gaps, coverage and truncation flag. Point values contain `avg,min,max,count` for CPU %, memory bytes, configured memory limit bytes (zero means unlimited), network RX/TX bytes per second, block read/write bytes per second and PIDs. Unsupported values are null. All series use a common bucket width; extrema and restart/connection segments survive aggregation. Pathological segment counts are bounded explicitly with `truncated=true`, while the summary still describes the entire valid query range.
- `GET /monitoring/instances/:instanceID/events?since=&until=&kind=&cursor=&limit=200` returns `{items,next_cursor?}`; limit is 1–256. Kinds are all/empty, lifecycle, health and abnormal. Cursor ordering uses event time and stable ID. Lifecycle, health, OOM and whitelisted attributes are persisted independently of notification suppression and replayed records are deduplicated.

Coverage distinguishes recorded, not_collected, excluded, expired, partial and truncated. Queries enforce retention immediately, even while physical cleanup is pending. Empty intervals, missing metrics, counter rollback and restart boundaries are not converted to zeros. Counter rates use consecutive valid observations and their actual interval; CPU is not capped at 100%. Only recorded valid samples contribute to summaries.

The control plane owns background per-container Stats streams and one monitoring event stream per explicit node runtime. Starts/stops/restarts/configuration changes reconcile streams, with periodic inventory checks, bounded opening concurrency/retries and a 12-batch write buffer. Agent monitoring admits at most 100 Stats streams per node, leaving transport capacity for ordinary operations. Container list reads reuse fresh samples; historical page requests never initiate persistent collection. Event reconnect coverage is explicitly uncertain because Docker only retains limited recent Engine events.

Migration `0002_container_monitoring.sql` is additive and uses the existing transactional migration ledger and advisory lock. Published baseline migrations remain unchanged. Samples use native UTC daily partitions, prepared ahead of time; fully expired partitions are removed, boundary records/events/gaps are deleted in bounded batches with a short lock timeout. Existing accounts, encrypted material, settings and task/audit/notification evidence are unaffected. No pre-feature history is fabricated; event replay is bounded by the current policy activation time, so disabling/excluding and then resuming does not import events from the excluded interval. Rollback stops collection and leaves the new evidence tables in place.

The UI provides a fixed one-hour list expansion (mobile Sheet), complete Monitoring tab, retained-instance history, up to four-container comparison, equal-duration period comparison, linked zoom/hover/events and CSV export of the current query resolution. Absolute/zoomed ranges pause following; preset ranges refresh every five seconds. CSV includes original UTC timestamps, identity, units, extrema/counts and empty values, with formula-safe quoting.

### Container files

All file routes require authentication and an explicit node and container ID. Their base is `/nodes/:nodeID/containers/:id/files`; no default-node alias exists. The container must be running and have `sh`, `stat`, `cat`, `sha256sum` for saves, and the standard file utilities used by the requested action. Operations run as the container's configured user through its Docker runtime, including TCP nodes; SUMA never opens bind source paths on its own host.

- `GET /files?path=/absolute/path&cursor=name` lists one directory page (200 entries), mount shortcuts, effective mount, and `next_cursor`. Entries identify regular files, directories, links, and special files; optional `executable=true` identifies regular files executable by the configured container user, and `has_error=true` identifies unresolved links (missing, inaccessible, or cyclic targets) or failed file metadata reads. These attributes are read through the node runtime without executing the listed files. Mounts report `type`, `source`/`name`, container `destination`, `read_write`, and `is_directory`.
- `GET /files/content?path=/absolute/file` reads UTF-8 regular text up to 2 MiB and returns `content`, SHA-256 `etag`, `persistent`, `read_only`, and `single_file_bind`. Writable container layers, tmpfs mounts, bind mounts, and Docker volumes can be edited using the container's configured user. A read-only root filesystem or mount remains read-only; `persistent=false` identifies content that is lost when its container or tmpfs is removed.
- `PUT /files/content` accepts `{ "path": "/absolute/file", "content": "...", "etag": "<previous hash>" }`. A mismatched hash returns HTTP 409. Saves verify the resulting content. Single-file bind mounts use a guarded in-place write and have a brief non-atomic window.
- `POST /files/actions` accepts `{ "action": "create_file|create_directory|rename|copy|move|delete", "path": "/absolute/path", "target": "/absolute/destination" }`. `target` is required for rename/copy/move; rename stays in the same parent directory. Existing targets are never overwritten. Copy, move, and delete return a node-scoped Task to poll through `/nodes/:nodeID/tasks/:taskID`; other actions return the resulting paths.
- `GET /files/history?path=/absolute/file` lists encrypted editor revision metadata. `GET /files/history/:revision?path=...` returns one revision's content. `POST /files/restore` accepts `{ "path": "...", "revision_id": 123, "etag": "<current hash>" }` and creates a new revision on success.

Paths must be normalized container-absolute paths. Symlink traversal and editing nonregular files are rejected. Destructive operations require UI confirmation. The history database keeps at most 20 revisions per file for 30 days and 512 MiB total, with daily pruning; content is AES-GCM encrypted and excluded from audit records.

Compose Project names and managed directories must match Docker Compose's lowercase native `[a-z0-9][a-z0-9_-]*` identity. Mixed-case input is rejected instead of silently rewritten, and runtime ownership is matched by exact native name.

Compose requests containing a `/var/run/docker.sock` bind are rejected by default. After two explicit UI warnings, the confirmed request carries `X-SUMA-Allow-Docker-Socket: true`; this authorizes only that request. TCP and Agent node binds must still use non-interpolated absolute source paths.

The resource routes listed below remain deprecated aliases for the migrated default node. `GET /health` reports only control-plane/database health; a disconnected Docker node does not make it fail. Legacy `node_id` filters validate that the node exists. Global `GET /tasks` and `GET /audit-logs` accept `scope=control_plane|all`. Tasks default to `control_plane`; audit logs default to `all`.

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

`GET /nodes/:nodeID/projects` derives Compose Projects from Docker `com.docker.compose.*` labels instead of runtime rows in PostgreSQL. Each response includes backend/scope identity, `source`, `managed`, capabilities, working-directory hints, and aggregated Service/Container Instance counts. SUMA-owned directories below the node Compose root keep a managed Project visible after `docker compose down` removes all labeled containers.

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

HTTPS token credentials use the supplied username exactly. An empty username defaults to `oauth2` for every host; there are no hosting-provider overrides. A hidden private repository may return 404 when permissions or credentials are wrong.

`custom_ca` is optional for any HTTPS Git remote using a private CA. Do not disable TLS verification. Credential secrets, private keys, passphrases, `known_hosts`, and CA contents are encrypted at rest and are never returned by the API. A `PUT` request may omit an existing sensitive value to preserve it.

Git, registry, and Docker TLS credential requests include `authorized_node_ids`; the default is empty. Registry credentials use `basic` (username and password) or `token` authentication and are passed through a temporary Docker config only when explicitly selected. Docker TLS credentials contain a CA, client certificate, and private key and are never returned after creation.

## Continuous delivery projects

The CD API manages independent Delivery Projects. A project may deploy Compose declarations and prebuilt images through the Compose adapter, but it is not a Compose project and has an independent lifecycle. It does not build source, run tests, publish images, or execute pipeline jobs.

Authenticated routes:

- `GET|POST /delivery-projects` lists or creates Delivery Projects. Full creation accepts `{ "name": "production", "configuration": <the configuration body below> }`; its transaction saves the project, explicit target nodes, repository, encrypted credentials and delivery policy together. Validation or persistence failure leaves no partial project or credential. The `201` response includes the project fields plus `configuration`; an automatically generated Webhook secret is returned once here and never by GET. Legacy name/`node_ids` creation remains supported.

Delivery Project responses include `deployment_name`, the saved Compose project name used on target nodes. New projects use the CD name exactly and accept 1–128 lowercase letters, digits, hyphens or underscores, starting with a letter or digit. Existing saved names and the legacy ID-based fallback remain stable. Target-node Project list/detail responses may include `delivery_project_name` as display metadata; their `name`, `native_name`, `ref`, routes and action targets retain the actual Docker Compose identity. First deployment checks for existing target-Engine containers, networks and volumes under the selected name and fails before pull/up on a conflict.

- `GET|DELETE /delivery-projects/:name` reads or deletes one project. Deletion requires `confirm=<project-name>`; optional `force=true` tears down its active runtime.
- `GET|PUT /delivery-projects/:name/configuration` reads or updates repository and delivery configuration.
- `PUT /delivery-projects/:name/automation` accepts `{ "enabled": false }` to pause polling and Webhook triggers, or `true` to resume. Returns the project with `automation_enabled` and records a control-plane audit. Requires a session; missing/non-boolean `enabled` returns 400, unknown projects return 404. It does not queue a task or change the saved delivery policy, credentials, target nodes, or release history. Queued automatic syncs recheck before fetching; started operations finish normally. Manual sync remains available while paused, creating candidates for explicit approval/deployment rather than deploying under Auto policy.
- `POST /delivery-projects/test-connection` (new draft) or `POST /delivery-projects/:name/test-connection` (existing project) reads remote refs with `{ "repository": { "clone_url": "https://git.example.test/team/repo", "ref_type": "branch", "ref": "main", "authentication": { "source": "none" } }, "node_ids": ["local"] }`. Uses draft credentials or retained project secrets without saving configuration, cloning, creating tasks/releases, or deploying. Center credentials require grants for the requested targets (saved targets when omitted on an existing project). Returns `{ "ref_checked": true, "commit_sha": "..." }` for a resolved branch/tag; annotated tags resolve to their commit. Commit refs return `ref_checked: false`: arbitrary commits and Compose files are verified during sync. Requests cancel on disconnect and time out after 30 seconds; failures return `422` (code `17715`) with sanitized Git diagnostics. Responses use `Cache-Control: no-store`.
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
  "automation_enabled": true,
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
- `automation_enabled` defaults to true for new and upgraded projects. Configuration updates that omit it retain its current value. Projects, configuration and `/cd/overview` expose it. A paused project's authenticated Webhook returns 200 with `{ "ignored": true, "reason": "automatic delivery is paused" }` without consuming its delivery ID or queuing work; invalid signatures still return 401. Paused events are not replayed automatically.
- `sync_interval_seconds` accepts 30 through 86400 seconds. The background reconciler scans for due Git projects at startup and every 15 seconds; manual and verified-webhook triggers are also available.
- `deployment_timeout` accepts 10 through 3600 seconds and is passed to Compose health waiting.

Before creating a release, SUMA enforces the deployment-source policy described below. Before every deployment or rollback it also verifies that the detached worktree is still at the recorded commit and contains no tracked modifications, untracked files, or ignored generated files. A dirty worktree fails the operation; SUMA does not reset or clean it automatically. Local `/compose` APIs never expose or mutate Delivery Projects.

### Git deployment-source policy

For Git delivery, every configured Compose/environment file, implicit project `.env`, and local file consumed by Compose must resolve inside the Git worktree, be a regular file, and be no larger than 2 MiB. This includes Compose `env_file`, `label_file`, and file-backed top-level `configs` and `secrets`; target-host bind sources follow the separate rules below.

SUMA rejects:

- Compose `include` and service `extends`;
- every service `build` declaration or service without a prebuilt `image`;
- source-file paths containing any `$` interpolation form;
- `privileged`, host network/PID/IPC/cgroup namespaces, host devices, inherited container volumes, high-risk capabilities, and disabled security confinement;
- a Docker socket mount;
- writable bind mounts, non-absolute rendered bind sources, or protected host paths.

Named volumes remain supported. Read-only bind sources are target Docker-host paths for Unix Socket, TCP and Agent nodes; they need not exist in SUMA's filesystem or remain inside the Git worktree. TCP/Agent targets also require non-interpolated absolute sources in the original Compose files. The target Engine handles source existence and mounting. Compose source/environment/config/secret file checks remain separate; see [CD-DESIGN.md](CD-DESIGN.md#deployment-source-policy).

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

## Notifications

All routes require an authenticated session and the usual Origin check for writes. Secrets are write-only; blank fields retain encrypted material. Channel/rule writes carry an optimistic `version`. See [channel setup and provider permissions](doc/notifications.md).

Supported providers are `feishu_app`, `telegram` and `webhook`. Each Feishu App ID has one channel and one outbound connection, with `config.targets: [{"chat_id":"oc_...","name":"Operations"}]` (up to 50 unique recipients). Enabled Feishu channels discover human conversations through private messages and group mentions, including non-text messages. Discovery persists metadata and deduplication only, never message bodies, and does not subscribe recipients or execute commands.

Telegram uses `config.chat_id` as the destination. Its `config.auto_discover` boolean defaults to false; opt-in enables long polling for recipient discovery. Existing platform webhooks are preserved. Feishu always discovers while enabled; the option is false for Feishu and Webhook channels. Pausing/deleting a channel, replacing credentials, disabling discovery or stopping the service cancels the connection and rejects events from obsolete connection generations.

Rules are reusable filters with no enabled/paused state. Rule writes accept `name`, `version` and filtering `config`; responses add the rule ID and timestamps. Selecting and saving a rule in an enabled channel or conversation subscription activates it there. Their `config` contains only `events`, `severities`, `node_ids`, `group_ids` and `projects`. A rule can be saved without a channel. Recovery events are selected directly, without a separate recovery flag. Legacy rule enabling, routing and policy fields are rejected.

Channels hold `config.subscription: { "rule_ids": ["rule-id"], "policy": { ... } }`. The Feishu UI labels the channel-level default as **All conversations** (全部对话), applying to selected conversations unless overridden. A Feishu target may include `subscription_override` with the same shape; absent/null inherits the whole channel default. Switching to an override copies defaults, while subsequent changes remain independent. Empty rule selection generates no automatic deliveries. Defaults and overrides save atomically with the channel version. The policy contains `mode` (`immediate`, `merge`, `daily`), `timezone`, `digest_hour`, `quiet_start`, `quiet_end`, optional `muted_until`, `template`, `fallback_id` and `fallback_chat_id`. A fallback must use another channel and a configured recipient. Policy timezone governs scheduling and rendered timestamps; channel language/public URL are snapshotted for rendering.

Any subscribed rule can match. The same event/channel/recipient has one automatic delivery claim, protected by PostgreSQL transaction locks and a unique index. Primary and fallback routes share claims. Batches group by recipient, due time and immutable configuration snapshot. Rule/subscription/policy edits and deletions affect new events only; queued messages retain their original snapshot and no historical events are backfilled. Pausing/deleting a channel or removing/replacing recipients cancels pending/retry deliveries. Rule deletion detaches all current bindings and increments affected channel versions; removed fallback destinations similarly detach invalid references.

Deliveries return `matched_rules: [{"id":"rule-id","name":"Historical name","version":1}]`, `event_count`, `source` (`event`, `fallback`, `manual`) and optional `origin_delivery_id`, replacing the old `rule_id`. Per-event rule evidence and private snapshots survive rule deletion. Manual resend creates a separate delivery with the original snapshot and does not consume an automatic claim. Fallback receives the original message immediately after attempts are exhausted, without rule rematching or recursive fallback; failure and fallback enqueue commit atomically.

Connection state is held in memory. Feishu returns `message_count`, `last_message_at`, `last_message_result` and a safe `discovery_error`; socket readiness does not prove published event subscriptions. Matching critical events and CD awaiting-approval notifications bypass quiet periods and digest delays. Cards provide notification text and an optional SUMA URL without action callbacks. Provider delivery remains at-least-once after ambiguous timeouts.

| Method | `/api/v1` path | Behavior |
| --- | --- | --- |
| GET | `/notifications/catalog` | Canonical events and rule presets |
| GET / POST | `/notifications/channels` | List or create a channel |
| PUT / DELETE | `/notifications/channels/:id` | Edit/pause or delete a channel |
| POST | `/notifications/channels/:id/check` | Check credentials; connection and delivery are separate |
| POST | `/notifications/channels/:id/test` | Send an explicit test, including while paused; Feishu requires a configured `chat_id` |
| GET | `/notifications/channels/:id/chats` | Discovered recipient conversations |
| GET | `/notifications/channels/:id/connection` | Feishu connection state and discovery observations |
| GET / POST | `/notifications/rules` | List or create a reusable event filter |
| PUT / DELETE | `/notifications/rules/:id` | Edit or remove a rule |
| GET | `/notifications/inbox` | Latest 200 historical messages and per-user unread count; ongoing fault evidence includes `occurrences`, `last_occurred_at`, optional `resolved_at`; recovery events include original IDs in `recovery_of` |
| POST | `/notifications/inbox/:id/read` | Persist per-user read state |
| GET | `/notifications/deliveries` | Latest 200 deliveries and suppression reasons |
| POST | `/notifications/deliveries/:id/resend` | Queue a separate manual delivery using the original snapshot |

Delivery evidence includes `chat_id`. Schema incompatibility returns HTTP 503 / `20802`; the current legacy message mentions fresh initialization, but it does not authorize resetting production data. Production is live: future upgrades must retain existing state through new versioned SQL migrations, preserving published migration files and historical evidence. The application does not automatically delete existing data.
