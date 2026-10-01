# Docker cleanup smoke verification

Run from the repository root on a Linux Docker host with Go and the Docker Compose plugin. The suite refuses ordinary host endpoints: every resource operation must target a disposable Docker-in-Docker daemon whose name and socket path use the prefixes below. Agent containers run on the outer host and forward only this disposable socket.

Prepare the images once:

```bash
docker pull docker:27-dind
docker pull alpine:3.24
docker build -f Dockerfile.agent -t suma-agent:cleanup-smoke .
```

Provision, verify, and remove only the fixtures created by this shell:

```bash
set -eu
cleanup_root=$(mktemp -d /tmp/suma-cleanup-live.XXXXXX)
cleanup_daemon=suma-cleanup-isolated-$(date +%s)-$$
trap 'docker rm -f -v "$cleanup_daemon" >/dev/null 2>&1 || true; rm -rf "$cleanup_root"' EXIT
mkdir -p "$cleanup_root/run" "$cleanup_root/certs"
docker run -d --privileged --name "$cleanup_daemon" \
  --label suma.cleanup.protect=true \
  -e DOCKER_TLS_CERTDIR=/certs -p 127.0.0.1::2376 \
  -v "$cleanup_root/run:/var/run" -v "$cleanup_root/certs:/certs" \
  docker:27-dind >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$cleanup_daemon" docker info >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$cleanup_daemon" docker info >/dev/null
cleanup_port=$(docker inspect --format '{{(index (index .NetworkSettings.Ports "2376/tcp") 0).HostPort}}' "$cleanup_daemon")
docker save alpine:3.24 -o "$cleanup_root/alpine.tar"
docker exec -i "$cleanup_daemon" docker load < "$cleanup_root/alpine.tar" >/dev/null
SUMA_RUN_CLEANUP_SMOKE=1 \
SUMA_CLEANUP_SMOKE_UNIX="unix://$cleanup_root/run/docker.sock" \
SUMA_CLEANUP_SMOKE_TCP="tcp://127.0.0.1:$cleanup_port" \
SUMA_CLEANUP_SMOKE_CERTS="$cleanup_root/certs/client" \
SUMA_CLEANUP_SMOKE_DIND="$cleanup_daemon" \
SUMA_AGENT_SMOKE_IMAGE=suma-agent:cleanup-smoke \
go -C server test -tags dockersmoke ./internal/api \
  -run '^TestRealDockerCleanupTransports$' -count=1 -v
```

The daemon uses a private Unix socket and loopback-only mTLS port. The Agent test creates a temporary HTTPS server with a verified CA and places its enrollment token in a private environment file; it never prints the token. Test teardown removes fixture Agent containers and their own anonymous state volumes. No host-wide prune is used.

Each Unix, mTLS TCP and verified Agent case exercises authenticated policy/preview/run APIs, non-force container/image/network removal, stopped and running references, managed Compose declarations, resources added after preview, races that make networks used, protected and typed-name volume deletion, Task results, audits, and Engine-reported cache reclamation. Retention decisions for newly created fixtures use an injected future service clock; Engine cache reclamation is verified separately with a fresh disposable cache and a zero-day adapter option. Production policies enforce at least one retention day. Older/newer cache parameter selection and unsupported APIs are covered by daemon-free adapter tests.
