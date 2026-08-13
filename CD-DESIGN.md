# Continuous delivery with a Compose deployment adapter

## Purpose and scope

DockPort continuously delivers an already deployable Docker Compose declaration from Git to one or more selected Docker nodes. Git is the desired-state source; each exact Git commit, rendered Compose configuration, and immutable target-node snapshot becomes a traceable release.

This capability is CD only. DockPort does not:

- compile application source or execute repository-provided scripts;
- run unit, integration, end-to-end, or security tests;
- build, scan, sign, or publish container images;
- orchestrate GitHub Actions, GitLab pipelines, build runners, or build-status gates;
- deploy through remote agents, SSH, clusters, Swarm, or Kubernetes.

An external build system may create the referenced images, but DockPort neither integrates with nor depends on that system. Its contract begins when a repository commit contains valid Compose files that reference pullable images.

## Domain ownership

DockPort models local Compose orchestration and continuous delivery as independent domains:

| Domain | Aggregate root | Owned lifecycle | Compose relationship |
| --- | --- | --- | --- |
| Compose | Compose Project | Local files, validation, and runtime actions | Owns its editable local Compose declaration |
| Continuous Delivery | Delivery Project | Repository, credentials, synchronization, releases, approvals, deployment, drift, and rollback | Invokes Compose as a deployment adapter for a release |

There is no shared source-mode switch. Creating a Compose Project never creates a Delivery Project, and creating a Delivery Project never inserts an item into Compose. Repositories and detached worktrees live below the Git root. Every Delivery Project owns a stable Compose runtime namespace, preventing both commit-specific worktrees and similarly named local Compose projects from changing or colliding with its deployment identity.

## Product surfaces

Compose and continuous delivery are separate navigation surfaces:

- **Compose** owns locally authored Compose files, validation, service/log views, and lifecycle actions.
- **Continuous Delivery** owns project creation, Git onboarding and repository credentials, clone URL/ref configuration, synchronization, reconcile mode, approvals, release history, deployment, drift, rollback, and webhook setup.

The Compose workspace does not call CD project, configuration, drift, release, approval, deployment, rollback, or synchronization endpoints. The CD workspace does not use the Compose project list as its catalog.

## Generic Git repository model

Repository configuration has no hosting-provider selector. A single `clone_url` accepts any standards-compatible HTTPS, `ssh://`, or SCP-style Git remote, including public services, self-managed forges, and bare SSH servers. Authentication is selected only from the transport: public/HTTP credentials for HTTPS or an SSH key with pinned `known_hosts` for SSH. A credential-scoped private CA supports internal HTTPS services.

Webhook payload formats are request adapters rather than repository configuration. The single project webhook URL recognizes GitHub-compatible and GitLab-compatible push headers, with a generic bearer-authenticated JSON form as the universal fallback. DockPort does not call provider APIs or infer external pipeline status.

## Repository configuration

A Delivery Project records:

| Value | Contract |
| --- | --- |
| `clone_url` | HTTPS, `ssh://`, or SCP-style SSH URL without embedded password/token |
| `credential_id` | Reference to encrypted credential material; optional for public remotes |
| `ref_type` | `branch`, `tag`, or `commit` |
| `ref` | Valid Git ref or a full commit SHA |
| `compose_files` | Nonempty ordered list of paths relative to the repository root; files may be in different directories |
| `environment_file` | Optional path relative to the repository root for Compose interpolation |
| `reconcile_mode` | `observe`, `manual`, or `auto` |
| `sync_interval_seconds` | 30–86400 seconds; persisted for bounded reconciliation scheduling |
| `deployment_timeout` | 10–3600 seconds for Compose `--wait-timeout` |
| `auto_rollback` | Whether a failed new delivery may attempt to restore the previous successful release |

All configured file paths are relative to the repository root. DockPort evaluates symlinks and rejects a Compose file or environment file that resolves outside its detached worktree. Compose files are passed in list order; the first file establishes the Compose project directory for relative paths, and later files override or extend earlier files using Docker Compose merge rules. Deployment files must be regular files no larger than 2 MiB. Clone URL validation rejects embedded passwords, local filesystem remotes, `file://`, `git://`, external Git transports, query strings, fragments, and traversal-like repository paths.

## Credential model

Credential types are:

- `none`: no secret material;
- `http_token`: token/password supplied through Git AskPass, with an optional username;
- `http_basic`: explicit username and password/token through Git AskPass;
- `ssh_key`: private key plus mandatory pinned `known_hosts`, with an optional passphrase.

A credential may also contain a custom CA certificate for an internal HTTPS Git service. DockPort does not provide a skip-TLS-verification switch.

Sensitive fields are encrypted with AES-GCM before SQLite storage. A 32-byte key is loaded from `DOCKPORT_SECRET_KEY_FILE`; when absent it is generated atomically with file mode `0600`. The database stores only ciphertext and a non-secret fingerprint. API responses expose metadata but never plaintext.

For each Git operation DockPort creates a private temporary directory, writes only the needed AskPass, CA, SSH key, and `known_hosts` files, disables terminal prompts with `GIT_TERMINAL_PROMPT=0`, and removes the directory afterward. Known secrets are redacted from captured Git output. A token is never embedded in the stored or logged clone URL.

The database and encryption key form one recovery set. Losing the key makes stored credentials and webhook secrets undecryptable; exposing it together with the database exposes them. Back up and restrict them accordingly.

## Synchronization and release creation

The synchronization transaction is deliberately separate from deployment:

1. An authenticated webhook, periodic reconciliation, or authenticated manual request queues a task.
2. DockPort takes the per-project lock and reloads project configuration.
3. The common Git client clones when necessary, then updates the remote URL and fetches branches/tags.
4. The configured branch, tag, or commit is resolved to an exact commit SHA.
5. DockPort creates or reuses a detached worktree named by that SHA.
6. It verifies that the worktree is inside the configured Git root, `HEAD` matches the SHA, and Git reports no tracked changes, untracked files, or ignored files.
7. It resolves repository-root-relative Compose files and the optional env file, then enforces deployment-source policy and worktree containment.
8. `docker compose config --quiet` validates syntax and interpolation.
9. `docker compose config --format json` produces the canonical rendered model, which is checked by the rendered deployment policy.
10. DockPort hashes that rendered model, extracts referenced images, and deduplicates an already reconciled `commit SHA + config hash`.
11. It creates a release whose commit/config identity is fixed and links it to the previous active release, task, trigger, actor, and worktree.

Configuration validation is a deployment preflight. It does not execute service commands or arbitrary repository code. Worktree verification is fail-closed: DockPort never runs `git reset` or `git clean` to repair a contaminated release directory. Removing the Delivery Project is the operation that removes its complete Git clone/worktree directory; it does not remove a local Compose Project.

### Deployment-source policy

The source policy runs before Compose rendering:

- each configured Compose file, configured environment file, and implicit project `.env` must be a regular file of at most 2 MiB;
- Compose `include` and service `extends` are rejected;
- service `build` is rejected because every service must consume a prebuilt image;
- local paths referenced by service `env_file` or `label_file`, and by file-backed top-level `configs` or `secrets`, must resolve inside the worktree as regular files of at most 2 MiB;
- source paths containing any `$` interpolation form are rejected rather than resolved against those values.

After Compose produces JSON, the rendered policy requires at least one service and a nonempty image for every service. It rejects privileged mode, host network/PID/IPC/cgroup namespaces, host devices, inherited container volumes, high-risk `ALL`/`SYS_ADMIN` capabilities, disabled security confinement, Docker socket mounts, and all writable bind mounts. A bind source must already exist, resolve inside the worktree, and be read-only. Named volumes are allowed and are the supported persistent-data mechanism.

### Reconcile modes

- `observe`: synchronize and expose drift/release information; approval, deployment, and rollback are blocked.
- `manual`: prepare an `awaiting_approval` release; an authenticated user may approve/reject it and explicitly deploy it.
- `auto`: immediately deploy a newly validated release from the same serialized task.

The background reconciler runs once at application startup and scans every 15 seconds. A configured Delivery Project becomes due according to its `sync_interval_seconds` value (30–86400 seconds) and last-sync timestamp. Manual sync and webhooks remain independent triggers. The project reservation rejects overlapping queued operations rather than accumulating an unbounded per-project task backlog.

## Webhook trust boundary

All webhook adapters use:

```text
POST /api/v1/webhooks/git/:hookID
```

The endpoint is not protected by a DockPort session because Git servers call it directly. Security is supplied by an unguessable hook ID, a strong per-project secret, HTTPS at the reverse proxy, payload verification, repository/ref matching, and delivery deduplication.

Common behavior:

- cap the raw request body at 2 MiB;
- verify the signature/token before processing event data;
- detect only supported signed push/event forms from request headers;
- compare canonical repository identity and configured branch/tag;
- persist `(hook ID, delivery ID)` as a unique idempotency key;
- enqueue a task and return `202`, rather than deploying in the HTTP request;
- ignore payload file contents and independently fetch the stored remote/ref.

Request-format verification:

| Request format | Authentication | Delivery ID | Accepted event |
| --- | --- | --- | --- |
| GitHub | `X-Hub-Signature-256` HMAC-SHA256 | `X-GitHub-Delivery` | Push |
| GitLab | standard `X-Gitlab-Token`, or HMAC plus a fresh timestamp from a compatible sender | Webhook/Event UUID | Push Hook, Tag Push Hook |
| Generic | `Authorization: Bearer …` | `Idempotency-Key`, or body hash fallback | Generic trigger |

The generic JSON body identifies the repository and may contain a ref. It cannot select a command, Compose path, environment file, image, or commit for deployment.

## Deployment

A prepared release is delivered under the same per-project reservation and lock. After one approval, DockPort runs the following flow concurrently for every node in the release target snapshot:

1. Re-verify that the release worktree is at the recorded commit and clean; reject it if it was modified since synchronization.
2. Mark the release `pulling` (or `rolling_back`).
3. Run Compose with an explicit project name, worktree project directory, ordered `--file` values, and optional `--env-file`.
4. Pull all declared images with the always-pull policy.
5. Run `docker compose up -d --remove-orphans --wait --wait-timeout <seconds>`.
6. Query `docker compose ps --format json --all`; every service must be running and, if it reports health, healthy.
7. Attach the observation to that node's Deployment and update only that node's active target state after success.
8. Aggregate the Release as `succeeded`, `partial_failed`, `failed`, or `rolled_back`, while retaining every child task and outcome.

`--wait` can only use the health semantics available in the Compose file. A service without a healthcheck may be considered ready when its container is merely running. Production repositories should define meaningful healthchecks for critical services.

Images may use tags or immutable digests. Digests are recommended for reproducibility, but DockPort currently records and pulls the declaration; it does not build, scan, sign, or certify an image.

## Release and drift model

Each release records repository URL, ref, exact commit SHA, commit author/message, canonical config hash, image references, trigger, actor, task, Compose-source snapshot fields, approval, and immutable target snapshots. Each target Deployment records its node/name snapshot, child task, previous active Release, timing, failure reason, health observation, rollback attempt, and result.

Relevant statuses are:

```text
awaiting_approval -> approved -> pulling -> deploying -> verifying -> succeeded
        |               |          \-------------------------------> failed
        `---------------+-> rejected

successful release selected -> new rollback release -> rolling_back
                               -> deploying -> verifying -> rolled_back / failed
```

Approval and rejection are synchronous state changes. Approval does not start delivery. The deploy endpoint accepts an explicitly `approved` release or retries a previously approved `failed` release. Auto mode proceeds directly from its newly prepared release without requiring a user approval record.

Drift is reported when no repository synchronization has happened, no release is active, the desired Git commit differs from the active release commit, or the active release has missing/not-running/unhealthy containers. Runtime container truth is read from Docker and is not mirrored into SQLite as desired state.

## Rollback boundary

Rollback means creating a new release record that redeploys an older release's recorded worktree and Compose file set. It is allowed only for a release that previously succeeded or was successfully restored. Observe mode blocks rollback. If the project is in auto mode, a manual rollback first persists `manual` mode so the reconciler cannot immediately replay the newer desired commit.

Rollback does not:

- delete or restore named volumes;
- execute `docker compose down -v`;
- reverse database migrations or mutate application data;
- change or revert the remote Git repository;
- guarantee compatibility between an old image and current persisted data.

A Compose service cannot use a writable bind mount in Git delivery. Use named volumes for writable persistent data; any bind mount must resolve inside the worktree and be read-only. Release identity protects the recorded Git/config reference; it is not an application-data snapshot.

If automatic rollback is enabled and a node deployment fails, DockPort restores that node's own previous active Release. Successful nodes remain on the new Release. Multi-node delivery is not atomic and does not attempt a cross-node transaction. Operators should disable automatic rollback for deployments with irreversible migrations and should revert/fix Git after restoration so desired and active state converge.

## Runtime configuration

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `DOCKPORT_GIT_COMMAND` | `git` | Single Git executable path/name used by the adapter |
| `DOCKPORT_GIT_ROOT` | `<data-root>/gitops` | Repository clones and detached worktrees |
| `DOCKPORT_SECRET_KEY_FILE` | `<data-root>/secret.key` | 32-byte AES-GCM key for credential and webhook secrets |
| `DOCKPORT_COMPOSE_COMMAND` | `docker compose` | Sole Compose process boundary |
| `DOCKPORT_COMPOSE_ROOT` | `<data-root>/compose` | Managed-mode Compose root |
| `DOCKPORT_DATA_ROOT` | `./data` | Base path for default persistent locations |

The production image includes Git, OpenSSH client tools, CA certificates, Docker CLI, and the Compose plugin. The Compose/Git data root is mounted at the same absolute host/container path because bind sources are interpreted by the host Docker daemon.

## Operational security checklist

- Terminate TLS at a trusted reverse proxy; enable secure session cookies.
- Never expose the Docker daemon on unauthenticated TCP 2375.
- Use a least-privilege repository token or read-only deploy key.
- Pin SSH hosts and use a private CA certificate instead of bypassing verification.
- Generate a unique high-entropy webhook secret per project and rotate it after exposure.
- Keep Git repositories free of plaintext deployment secrets; use named/external secret facilities appropriate to the deployment.
- Use named volumes for writable data; Git delivery rejects writable or out-of-worktree bind mounts.
- Review Compose privileges, host mounts, devices, ports, and Docker socket mounts before automatic delivery.
- Back up SQLite, `secret.key`, and required release/worktree data as one controlled recovery set.
- Treat access to DockPort and `/var/run/docker.sock` as root-equivalent host access.

## Delivery guarantees and non-guarantees

DockPort provides serialized per-project command execution, exact Git commit attribution, clean-worktree enforcement, canonical Compose configuration hashing, task progress, and an auditable active-release transition after successful Compose execution and runtime-state validation.

Docker Compose on one node is not a transactional deployment platform. A failed `up` can leave partially changed containers, service health depends on declared healthchecks, bind mounts and volumes can carry state across releases, and application migrations can be irreversible. Release history and rollback reduce operational risk but do not provide database backup, zero-downtime rollout, or cluster-grade reconciliation.
