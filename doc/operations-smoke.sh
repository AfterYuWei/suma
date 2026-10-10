#!/usr/bin/env bash
set -euo pipefail

# Image-update, aggregate-log, reviewed deployment and guest chat-query smoke.
# Only this disposable daemon and its anonymous volumes are removed on exit.
project_smoke_root=$(mktemp -d /tmp/suma-project-live.XXXXXX)
project_smoke_daemon="suma-project-isolated-$(date +%s)-$$"
cleanup_project_smoke() {
  docker rm -f -v "$project_smoke_daemon" >/dev/null 2>&1 || true
  rm -rf "$project_smoke_root"
}
trap cleanup_project_smoke EXIT

mkdir -p "$project_smoke_root/run" "$project_smoke_root/certs"
docker image inspect docker:27-dind >/dev/null
docker image inspect alpine:3.24 >/dev/null
docker image inspect "${SUMA_AGENT_SMOKE_IMAGE:-suma-agent:env-only-smoke}" >/dev/null
docker run -d --privileged --name "$project_smoke_daemon" \
  -e DOCKER_TLS_CERTDIR=/certs -p 127.0.0.1::2376 \
  -v "$project_smoke_root/run:/var/run" \
  -v "$project_smoke_root/certs:/certs" docker:27-dind >/dev/null
for project_smoke_attempt in $(seq 1 60); do
  if docker exec "$project_smoke_daemon" docker info >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$project_smoke_daemon" docker info >/dev/null
project_smoke_port=$(docker inspect --format '{{(index (index .NetworkSettings.Ports "2376/tcp") 0).HostPort}}' "$project_smoke_daemon")
docker save alpine:3.24 -o "$project_smoke_root/alpine.tar"
docker exec -i "$project_smoke_daemon" docker load < "$project_smoke_root/alpine.tar" >/dev/null
docker exec "$project_smoke_daemon" docker run -d --name suma-target-query-smoke alpine:3.24 sleep 600 >/dev/null

GIN_MODE=release \
SUMA_RUN_OPERATIONS_SMOKE=1 \
SUMA_RUN_PROJECT_SMOKE=1 \
SUMA_PROJECT_SMOKE_UNIX="unix://$project_smoke_root/run/docker.sock" \
SUMA_PROJECT_SMOKE_TCP="tcp://127.0.0.1:$project_smoke_port" \
SUMA_PROJECT_SMOKE_CERTS="$project_smoke_root/certs/client" \
SUMA_PROJECT_SMOKE_DIND="$project_smoke_daemon" \
SUMA_AGENT_SMOKE_IMAGE="${SUMA_AGENT_SMOKE_IMAGE:-suma-agent:env-only-smoke}" \
GOCACHE="${SUMA_PROJECT_SMOKE_GO_CACHE:-/tmp/suma-project-go-cache}" \
go -C server test -tags dockersmoke ./internal/api ./internal/app \
  -run '^(TestRealDockerProjectConfigurationTransports|TestRealDockerGuestChatQueryNamedNode|TestRealDockerBoundChatStreamsNodeStatus|TestRealDockerAIContainerQueryClarifiesTarget)$' -count=1 -timeout 9m -v
