# SUMA

**Language: [简体中文](../README.md) | [English](README.en.md)**

SUMA is a single monolithic control plane for multi-node Docker management: manage all your Docker Engines from one web UI — no agents on your servers, no SSH sessions, no Swarm or Kubernetes. Built for personal servers, HomeLabs, NAS devices, VPS hosts, and small teams.

## Features

### Multi-node engine access

- Agentless: attach mounted Unix sockets to reach local/host engines, or add remote Docker TCP endpoints
- TCP connections enforce mutual TLS by default; plaintext TCP is only accepted for loopback addresses. TLS material is stored encrypted and authorized per node
- Global node switcher: flip the active node from the header; resources, Compose, and tasks all follow
- Automatic probing: node status and latency refresh every 30 seconds with graceful degradation
- Bind allowlists: on TCP nodes, Compose mount sources must stay within the node's approved host directories

### Fleet overview

- Control-plane layer: global metric cards, per-node container/image/CPU/memory totals, CD release and drift states
- Node layer: host resource usage, container details, engine version and uptime — click a row to switch to that node

### Containers and resources

- Dense lists with inline actions: start/stop/restart/remove, always behind destructive confirmations
- Container detail: Inspect (sensitive environment values masked automatically), live log streaming, an xterm.js interactive terminal (with resize), and ECharts realtime stats
- Images: pull progress streaming, tagging, removal, private registry credential support
- Networks / volumes: full lifecycle management; volume removal checks usage and refuses to delete in-use volumes

### Compose projects

- Local projects: edit compose.yml and .env in Monaco with validation before saving
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
- Chinese/English interface, dark/light/system themes, and the `Ctrl/Cmd+K` command palette

## Quick start

Prerequisite: one Linux host running Docker Engine. To manage engines on other machines, see "Adding nodes" below.

### Option 1: Docker Compose (recommended)

Save this as `docker-compose.yml`:

```yaml
services:
  suma:
    image: ghcr.io/afteryuwei/suma:stable # pin a version tag like :0.1.0 for fixed releases
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      SUMA_DATA_ROOT: "${SUMA_DATA_PATH:-/opt/suma/data}"
    volumes:
      # The data directory must be bind-mounted at the same absolute path so
      # relative bind mounts inside managed Compose projects resolve on the host.
      - ${SUMA_DATA_PATH:-/opt/suma/data}:${SUMA_DATA_PATH:-/opt/suma/data}
      - /var/run/docker.sock:/var/run/docker.sock
```

Start it:

```bash
mkdir -p /opt/suma/data && docker compose up -d
```

Custom data location:

```bash
SUMA_DATA_PATH=/srv/suma mkdir -p $SUMA_DATA_PATH
SUMA_DATA_PATH=/srv/suma docker compose up -d
```

### Option 2: docker run

```bash
mkdir -p /opt/suma/data

docker run -d --name suma \
  --restart unless-stopped \
  -p 8080:8080 \
  -e SUMA_DATA_ROOT=/opt/suma/data \
  -v /opt/suma/data:/opt/suma/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/afteryuwei/suma:v0.1.0
```

### First use

Open `http://<host-ip>:8080`, create the administrator account, and sign in.

**Common environment variables**

| Variable | Default | Purpose |
| --- | --- | --- |
| `SUMA_DATA_ROOT` | `/opt/suma/data` (container convention) | Root for data and credentials; all other paths derive from it by default |
| `SUMA_ADDRESS` | `:8080` | Listen address (map the host port accordingly) |
| `SUMA_COOKIE_SECURE` | `false` | Set `true` when deployed behind HTTPS |
| `SUMA_DOCKER_HOST` | `unix:///var/run/docker.sock` | Engine address used only for first-run node bootstrap |

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
make dev           # local development mode (web 5173 / server 8081)
make docker-up     # build and start the production container
```

Quality checks: `make check` (backend `go test ./...` + `go build ./...`; frontend lint/typecheck/build).

## Adding nodes

1. Open the Nodes page and add a node:
   - **Unix Socket**: mount the target machine's `/var/run/docker.sock` into the SUMA container at any path, then register that path;
   - **Docker TCP**: enter the remote endpoint such as `tcp://192.168.1.99:2376`, choose mTLS, and attach a Docker TLS credential.
2. Use Test Connection to verify reachability and latency.
3. Configure bind allowlists for TCP nodes to constrain which host directories their Compose projects may mount.

> Security note: never expose an unauthenticated Docker API (plaintext 2375) on a network. Always use mTLS for remote access. For public deployments, put SUMA behind an HTTPS reverse proxy and enable `SUMA_COOKIE_SECURE=true`.

## Data and backups

- `/opt/suma/data` (or your custom directory) holds the SQLite database, local Compose projects, Delivery Project Git worktrees, and the credential-encryption key `secret.key`
- Back up the whole directory before upgrades or migrations; losing `secret.key` makes stored credentials undecryptable

## Documentation

- [PLANS.md](../../PLANS.md): feature progress and pre-launch verification log
- [ARCHITECTURE.md](../../ARCHITECTURE.md): architecture overview
- [API.md](../../API.md): REST / WebSocket API reference
- [CD-DESIGN.md](../../CD-DESIGN.md): continuous delivery design model
