# SUMA API

All REST responses use `{ "code": 0, "message": "success", "data": ... }`. Errors use a nonzero code and an appropriate HTTP status. Authentication uses the `suma_session` HttpOnly cookie.

## REST `/api/v1`

Node-aware clients use these routes:

- `GET|POST /nodes`, `GET|PUT|DELETE /nodes/:nodeID`, `POST /nodes/:nodeID/test`
- `GET /nodes/:nodeID/{overview,docker/info}`
- `GET|POST|PUT|PATCH|DELETE /nodes/:nodeID/{containers,images,networks,volumes,compose}/...`
- `POST /nodes/:nodeID/system/prune`

The resource routes listed below remain deprecated aliases for the migrated default node. `GET /health` reports only control-plane/database health; a disconnected Docker node does not make it fail. `GET /tasks` and `GET /audit-logs` accept `node_id` filters.

- `GET /health`, `GET /docker/info`
- `GET /auth/status`, `POST /auth/initialize`, `POST /auth/login`, `POST /auth/logout`, `GET /auth/session`
- `GET /containers`, `GET /containers/:id`, `POST /containers/:id/{start|stop|restart|pause|unpause|kill}`, `PATCH /containers/:id`, `DELETE /containers/:id`
- `GET /images`, `GET /images/:id`, `POST /images/pull`, `POST /images/:id/tag`, `DELETE /images/:id`
- `GET|POST /networks`, `GET|DELETE /networks/:id`
- `GET|POST /volumes`, `GET|DELETE /volumes/:id`
- `GET|POST /compose`, `POST /compose/batch`, `GET|PUT|DELETE /compose/:name`, `GET /compose/:name/services`, `GET /compose/:name/logs`, `POST /compose/:name/validate`, `POST /compose/:name/{up|down|start|stop|restart|pull|build|update}`
- `GET /overview` for live host CPU, memory, disk, network, uptime, platform, and Docker summary data
- `GET /tasks`, `GET /tasks/:id/logs`, `POST /tasks/:id/cancel`
- `GET /audit-logs`, `GET|PUT /settings`
- `POST /system/prune` starts a confirmed task for unused containers, networks, dangling images, and anonymous volumes

`DELETE /compose/:name` requires `confirm=<project-name>`. Adding `force=true` first runs `docker compose down --remove-orphans --timeout 0 --volumes`, then removes only the local Compose project. Named volumes and their data are deleted by default; add `preserve_volumes=true` to omit `--volumes` and keep them. This operation never changes a Delivery Project.

`POST /compose/batch` accepts `{ "names": ["api", "worker"], "action": "restart" }` for 1–100 projects. Supported actions are `start`, `stop`, `restart`, `update`, and `down`. Each valid project starts an independent asynchronous task; the response reports `name`, `task_id`, and `success` per project so one failure does not block the remaining projects.

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

Deployment pulls declared images, runs `docker compose up -d --remove-orphans --wait`, then requires every reported service to be running and, when a health status exists, healthy. Drift reports both Git commit differences and missing/unhealthy active containers.

Rollback creates a new release record from a previously `succeeded` or `rolled_back` release; it does not rewrite the old record. If the project was in `auto` mode, a manual rollback first changes it to `manual` so polling cannot immediately redeploy the newer Git revision. A failed `docker compose up` may trigger automatic restoration when `auto_rollback` is enabled; that path also switches reconciliation to `manual`. Re-enable `auto` explicitly only after Git and the desired runtime state have been reconciled.

## WebSocket

- `/ws/containers/:id/logs` streams timestamped UTF-8 log chunks.
- `/ws/containers/:id/stats` streams Docker Stats JSON samples.
- `/ws/containers/:id/terminal` streams binary terminal output and accepts binary input or JSON `{type:"input",data}` / `{type:"resize",cols,rows}`.
- `/ws/tasks/:id` replays retained task logs and streams progress/status events, including CD sync, delivery, and rollback tasks.

All WebSockets require the same session cookie as REST. Disconnecting cancels the underlying context and closes Docker streams or exec sessions.

Node-aware realtime routes are `/ws/nodes/:nodeID/containers/:id/{logs,stats,terminal}`. Legacy container WebSockets are default-node aliases.
