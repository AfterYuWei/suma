# SUMA

**Language: [简体中文](../README.md) | [English](README.en.md)**

SUMA is a single control plane for multi-node Docker management. Manage Engines from one web UI through direct Docker connections or an outbound Agent container, without SSH, Swarm, or Kubernetes. Built for personal servers, HomeLabs, NAS devices, VPS hosts, and small teams.

## Features

### Multi-node engine access

- Three connection types: mounted Unix sockets, direct Docker TCP, or an Agent that connects outbound over HTTPS/WSS
- TCP connections enforce mutual TLS by default; plaintext TCP is limited to loopback, private-network, or Tailscale IP addresses and requires re-entering the target IP before saving
- Global node switcher: flip the active node from the header; resources, Compose, and tasks all follow
- Multi-membership Node Groups: nodes may belong to several Groups or no Group; Groups filter fleet and node choices, while only an explicit Node selection changes Docker context
- Automatic probing: node status and latency refresh every 30 seconds with graceful degradation
- Remote bind validation: Compose mount sources on TCP and Agent nodes must use non-interpolated absolute paths

### Fleet overview

- Control-plane layer: global metric cards, per-node container/image/CPU/memory totals, CD release and drift states
- Node layer: host resource usage, container details, engine version and uptime — click a row to switch to that node

### Containers and resources

- Dense lists with inline actions: start/stop/restart/remove, always behind destructive confirmations
- Container detail: Inspect (sensitive environment values masked automatically), live log streaming, an xterm.js interactive terminal (with resize), and ECharts realtime stats
- Images: pull progress streaming, tagging, removal, private registry credential support
- Networks / volumes: full lifecycle management; volume removal checks usage and refuses to delete in-use volumes

### Projects and Compose takeover

- One Projects entry: a current SUMA Project maps to one Docker Compose Project and exposes backend/capability-aware actions
- Project-level discovery groups every Service and Container Instance, including scale, drift, one-off, and orphan observations
- Takeover safely normalizes complete Local multi-file source or falls back for the whole Project to runtime reconstruction; TCP nodes use Inspect metadata only
- Review each environment value for compose.yml, plaintext `.env`, or exclusion; image-default values are excluded and sensitive values are masked
- Monaco editing and validation happen before atomic takeover; takeover itself never pulls, stops, or recreates existing containers
- Optional isolated preview is restricted to safely isolatable stateless drafts, uses a temporary `suma-preview-*` Project, and always cleans up without switching production traffic
- Git-sourced files are read-only: delivered Compose content cannot be tampered with and stays isolated from the CD domain
- Batch operations: multi-select then start/stop/restart/up/down once, with per-project results
- Expandable rows expose the runtime state: services, container status, per-container logs and terminal entry points

### Continuous delivery (CD)

- Connect any HTTPS/SSH Git repository (GitLab, GitHub, or self-hosted); credentials are AES-GCM encrypted at rest
- Webhook triggers: GitHub/GitLab-compatible push headers plus a generic signed fallback; scheduled polling sync also available
- Every delivery is an immutable Release: exact commit plus a fingerprint of the rendered Compose configuration, fully auditable
- Delivery modes: observe / manual approval / automatic, with parallel multi-node deployment and failed-node-only rollback
- Deployment health gates and drift detection keep the runtime reconciled with the desired state

### Authentication center

- Central management of Git credentials (token / basic / SSH deploy keys), registry credentials, Docker TLS material, and custom CAs
- Credentials deny every node by default and must be explicitly granted to projects or nodes; in-use credentials cannot be deleted

### Operations and security

- Task center: long-running operations (pulls, prune, deployments) persist as tasks with WebSocket progress and log streaming
- Audit log: every critical change records actor, action, target, and time
- System prune: disk usage preview plus double confirmation
- First-run administrator setup; bcrypt password hashing and HttpOnly SameSite session cookies
- Account center for avatar, profile, and password management, with username/email sign-in, authenticator-app TOTP, one-time recovery codes, and passwordless WebAuthn Passkeys
- Chinese/English interface, dark/light/system themes, and the `Ctrl/Cmd+K` command palette

## Quick start

Prerequisite: one Linux host running Docker Engine. To manage engines on other machines, see "Adding nodes" below.

### Option 1: Docker Compose (recommended)

Save this as `docker-compose.yml`:

```yaml
services:
  postgres:
    image: postgres:18.6
    restart: unless-stopped
    environment:
      POSTGRES_USER: suma
      POSTGRES_DB: suma
      POSTGRES_PASSWORD: ${SUMA_POSTGRES_PASSWORD:?Set a random hexadecimal password}
    volumes:
      - suma-postgres:/var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U suma -d suma"]
      interval: 5s
      timeout: 3s
      retries: 20
  suma:
    image: ghcr.io/afteryuwei/suma:stable
    container_name: suma
    restart: unless-stopped
    ports: ["8080:8080"]
    environment:
      SUMA_DATABASE_DSN: postgres://suma:${SUMA_POSTGRES_PASSWORD:?Set a random hexadecimal password}@postgres:5432/suma?sslmode=disable
    depends_on:
      postgres:
        condition: service_healthy
    volumes:
      - /Data:/Data
      - /var/run/docker.sock:/var/run/docker.sock
volumes:
  suma-postgres:
```

Start it:

```bash
# .env: SUMA_POSTGRES_PASSWORD=<random hexadecimal password, at least 32 characters>
mkdir -p /Data && docker compose up -d
```

To store data on another disk: make `/Data` a symlink to a target partition, or mount that partition directly at `/Data`; keep the path `/Data` on both sides.

### Option 2: docker run

Provision an empty PostgreSQL 18.6 database and write `SUMA_DATABASE_DSN` to a private `suma.env` file passed with `--env-file`. Verify server identity with TLS for remote databases. Missing DSNs, unavailable databases and failed migrations stop startup. Old development data is not migrated.

```bash
mkdir -p /Data

docker run -d --name suma \
  --restart unless-stopped \
  -p 8080:8080 \
  --env-file ./suma.env \
  -v /Data:/Data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/afteryuwei/suma:stable
```

### First use

Open `http://<host-ip>:8080`, create the administrator account, and sign in.

**Common environment variables**

| Variable | Default | Purpose |
| --- | --- | --- |
| `SUMA_DATA_ROOT` | `/Data` (baked into the production image; bare-metal runs default to `./data`) | Root for data and credentials; all other paths derive from it by default |
| `SUMA_DATABASE_DSN` | Required | PostgreSQL URL; never display or log credentials |
| `SUMA_ENV_FILE` | Discover `.env.local` | Select a private startup configuration file; `-` disables file loading; process environment takes precedence |
| `SUMA_ADDRESS` | `:8080` | Listen address (map the host port accordingly) |
| `SUMA_COOKIE_SECURE` | `false` | Optional force-on override; HTTPS browser access automatically uses Secure cookies |
| `SUMA_BROWSER_ORIGIN` | empty | Optional advanced origin restriction; normally the request host and port are checked automatically |
| `SUMA_TRUSTED_PROXIES` | empty | Trusted proxy IPs/CIDRs if client IPs behind a reverse proxy are needed; cannot be safely inferred |
| `SUMA_DOCKER_HOST` | `unix:///var/run/docker.sock` | Engine address used only for first-run node bootstrap |
| `SUMA_AGENT_PUBLIC_URL` | empty | Optional override; pairing defaults to the current HTTPS page origin. Set it only if the Agent needs a different reachable HTTPS origin |

**Image tags**

| Tag | Meaning |
| --- | --- |
| `0.1.0`, `v0.1.0` | Released builds created from git tags, kept permanently |
| `stable` | Tracks the latest release, overwritten on each new version |
| `abc1234` (short commit) | Preview build per main-branch commit, kept permanently |
| `pre` | Tracks the latest preview build, overwritten on each push |

### Build from source

```bash
git clone https://github.com/AfterYuWei/suma.git
cd suma
make install       # install frontend deps and Go modules
make local-config  # create .env.local and fill in your existing PostgreSQL credentials
# Native startup and tests read .env.local automatically (127.0.0.1:5432/suma).
# Optional bundled dev database: configure .env, run make db-up, use port 55432 in .env.local.
make dev           # local development mode (web 5173 / server 8081)
make docker-up     # build and start the production container
```

Quality checks: `make check` (backend `go test ./...` + `go build ./...`; frontend lint/typecheck/build).

## Adding nodes

1. Open the Nodes page and add a node:
   - You may first create one or more Node Groups and select multiple memberships in the node form. Groups organize and filter nodes; they are not clusters or batch execution targets.
   - **Unix Socket**: mount the target machine's `/var/run/docker.sock` into the SUMA container at any path, then register that path;
   - **Docker TCP**: enter the remote endpoint such as `tcp://192.168.1.99:2376`, choose mTLS, and attach a Docker TLS credential.
   - **Agent**: open SUMA over HTTPS and generate a one-time token valid for 10 minutes. The current page origin is used by default; set `SUMA_AGENT_PUBLIC_URL` if the Agent cannot reach it. You can select an existing Unix/TCP node for an in-place migration that preserves its ID and references.
2. Use Test Connection to verify reachability and latency.
3. Use explicit absolute host paths for bind mounts on TCP nodes; interpolated and relative sources are rejected.

### Deploy the Agent with Docker

The Compose file generated on the Nodes page already includes the one-time token in the `SUMA_AGENT_TOKEN` environment variable. Copy it to the Agent host as `docker-compose.yml`; do not commit a file containing the token. Enrollment tokens expire after 10 minutes and can be used only once. The paired Agent credential remains valid until revoked or replaced; token expiry does not affect automatic reconnection after Agent or control-plane restarts. Keep the suma-agent-data volume. If writing Compose manually, replace the placeholder below. The SUMA address must be reachable from the Agent over HTTPS. Your reverse proxy must allow WebSocket upgrades and long-lived connections. For a private CA, mount its PEM file read-only and set `SUMA_AGENT_CA_FILE`; TLS verification cannot be disabled.

```yaml
services:
  suma-agent:
    image: ghcr.io/afteryuwei/suma-agent:stable # pin the same release as SUMA in production
    container_name: suma-agent
    restart: unless-stopped
    environment:
      SUMA_AGENT_SERVER_URL: https://suma.example.com
      SUMA_AGENT_TOKEN: PASTE_ONE_TIME_TOKEN_HERE
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - suma-agent-data:/var/lib/suma-agent
volumes:
  suma-agent-data:
```

Run `docker compose up -d` and verify the node is online. You can then remove `SUMA_AGENT_TOKEN` from Compose and run the command again; the named volume stores the reconnect credential. With Docker enabled at boot and the Agent not manually stopped, `restart: unless-stopped` starts it automatically. Revoking the credential or manually refreshing the token invalidates the old credential and closes the Agent connection immediately. To pair again, generate a new token, update the environment variable, and redeploy the Agent; it exchanges the new token when its old credential is rejected. Compose files and the CLI remain on the SUMA control plane, so remote bind sources must be explicit absolute paths on the Agent host.

> Security: never expose an unauthenticated Docker API on a network. Direct TCP uses mTLS, while Agents use verified HTTPS/WSS. A Docker socket mounted `:ro` still grants full Docker control. Put public SUMA deployments behind HTTPS; browser access over HTTPS automatically uses Secure cookies.

## Data and backups

- PostgreSQL state lives in the `suma-postgres` volume. `/Data` independently holds Compose projects, Git worktrees and the credential-encryption key `secret.key`
- `/Data/compose/` contains managed Compose YAML, environment files and SUMA project metadata; `/Data/gitops/` contains CD repositories and revision worktrees; `/Data/backups/` is reserved and has no automated backup job
- Users, nodes, settings, notification configuration, audit, Tasks and Agent checkpoints live in PostgreSQL. Native `.env.local` startup configuration lives in the project root
- Back up the whole directory before upgrades or migrations; losing `secret.key` makes stored credentials undecryptable

## Documentation

- [PLANS.md](../../PLANS.md): feature progress and pre-launch verification log
- [ARCHITECTURE.md](../../ARCHITECTURE.md): architecture overview
- [API.md](../../API.md): REST / WebSocket API reference
- [PostgreSQL operations](postgresql.md): initialization, storage, configuration and verification commands
- [Notification center](notifications.md): channels, recipient discovery, rules and delivery verification
- [CD-DESIGN.md](../../CD-DESIGN.md): continuous delivery design model

## Scheduled Docker storage cleanup

Open **Settings → Storage cleanup → node menu → Manage cleanup** to configure each node independently. New policies are paused; the suggested schedule is Sunday 03:00 in the explicitly selected application timezone, or UTC when device timezone is automatic. Default cleanup retains seven days of dangling images and Engine builder cache with a best-effort 10 GiB budget. Stopped-container and unused-network removal start disabled.

**Settings → General settings → Timezone** defaults to following the browser device. Select an IANA zone to use it across project timestamps, logs, tasks, audits and other interface times. Saving updates the interface immediately and persists the choice. **Use system timezone** restores device following. Existing cleanup schedules retain their execution zone; next-run timestamps are displayed in the application zone.

Generate a five-minute preview before immediate execution. Enabling scheduling or expanding deletion requires the exact node name and automatic-deletion authorization. Managed Compose declarations, Compose resources, SUMA/Agent/builders, current CD releases and direct rollback/pending references are protected. Volumes are scanned only and require individual typed-name deletion confirmation. Image sizes are estimates with shared layers; cache reclamation is reported by Engine.

Pause stops future schedules; cancel the associated Task to stop subsequent steps of a running cleanup. Completed deletion cannot be undone. Offline/missed schedules and interrupted work are never replayed. Logs and independent Buildx builders are outside this feature's scope. See [API](../API.md#scheduled-docker-storage-cleanup) for endpoint contracts.
