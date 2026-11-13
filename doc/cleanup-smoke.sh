#!/usr/bin/env bash
set -euo pipefail

# All deletion/cache operations target this disposable Engine, never the host.
cleanup_smoke_root=$(mktemp -d "${TMPDIR:-/tmp}/suma-cleanup-live.XXXXXX")
cleanup_smoke_daemon="suma-cleanup-isolated-$(date +%s)-$$"
cleanup_smoke_fixtures() {
  docker rm -f -v "$cleanup_smoke_daemon" >/dev/null 2>&1 || true
  rm -rf "$cleanup_smoke_root"
}
trap cleanup_smoke_fixtures EXIT

mkdir -p "$cleanup_smoke_root/run" "$cleanup_smoke_root/certs"
docker image inspect docker:27-dind >/dev/null
docker image inspect alpine:3.24 >/dev/null
docker image inspect "${SUMA_AGENT_SMOKE_IMAGE:-suma-agent:cleanup-smoke}" >/dev/null
docker run -d --privileged --name "$cleanup_smoke_daemon" \
  --label suma.cleanup.protect=true \
  -e DOCKER_TLS_CERTDIR=/certs -p 127.0.0.1::2376 \
  -v "$cleanup_smoke_root/run:/var/run" \
  -v "$cleanup_smoke_root/certs:/certs" docker:27-dind >/dev/null
for cleanup_smoke_attempt in $(seq 1 60); do
  if docker exec "$cleanup_smoke_daemon" docker info >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$cleanup_smoke_daemon" docker info >/dev/null
cleanup_smoke_port=$(docker inspect --format '{{(index (index .NetworkSettings.Ports "2376/tcp") 0).HostPort}}' "$cleanup_smoke_daemon")
docker save alpine:3.24 -o "$cleanup_smoke_root/alpine.tar"
docker exec -i "$cleanup_smoke_daemon" docker load < "$cleanup_smoke_root/alpine.tar" >/dev/null

SUMA_RUN_CLEANUP_SMOKE=1 \
SUMA_CLEANUP_SMOKE_UNIX="unix://$cleanup_smoke_root/run/docker.sock" \
SUMA_CLEANUP_SMOKE_TCP="tcp://127.0.0.1:$cleanup_smoke_port" \
SUMA_CLEANUP_SMOKE_CERTS="$cleanup_smoke_root/certs/client" \
SUMA_CLEANUP_SMOKE_DIND="$cleanup_smoke_daemon" \
SUMA_AGENT_SMOKE_IMAGE="${SUMA_AGENT_SMOKE_IMAGE:-suma-agent:cleanup-smoke}" \
GOCACHE="${SUMA_CLEANUP_SMOKE_GO_CACHE:-/tmp/suma-cleanup-go-cache}" \
go -C server test -tags dockersmoke ./internal/api \
  -run '^TestRealDockerCleanupTransports$' -count=1 -timeout 9m -v
