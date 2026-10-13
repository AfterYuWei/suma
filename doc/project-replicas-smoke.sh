#!/usr/bin/env bash
set -euo pipefail

# Exercise the form's YAML edits against a disposable Docker engine.
replicas_repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
replicas_root=$(mktemp -d /tmp/suma-replicas-live.XXXXXX)
replicas_daemon="suma-replicas-isolated-$(date +%s)-$$"
cleanup_replicas_smoke() {
  docker rm -f -v "$replicas_daemon" >/dev/null 2>&1 || true
  rm -rf "$replicas_root"
}
trap cleanup_replicas_smoke EXIT

SUMA_REPLICAS_REPO="$replicas_repo" SUMA_REPLICAS_OUTPUT="$replicas_root" \
node --experimental-strip-types --input-type=module <<'JS'
import assert from 'node:assert/strict'
import { writeFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'
const { configureServiceReplicas, serviceReplicas, configValue } = await import(
  pathToFileURL(`${process.env.SUMA_REPLICAS_REPO}/web/src/features/compose/document.ts`)
)
const source = `# retained comment
services:
  api:
    image: alpine:3.24
    pull_policy: never
    command: [sleep, "300"]
    restart: unless-stopped
    stop_grace_period: 1s
    cpus: 0.1
    mem_limit: 16m
    x-preserved: stable
`
assert.equal(serviceReplicas(source, 'api').count, 1)
assert.equal(configureServiceReplicas(source, 'api', 1), source)
const legacy = source + '    deploy:\n      replicas: 2 # replica comment\n      resources: {limits: {cpus: "0.1", memory: 16m}}\n'
const both = source + '    scale: 2\n    deploy: {replicas: 2}\n'
let current = source
for (const [name, count, previous] of [
  ['default', 1], ['expanded', 3], ['reduced', 2], ['zero', 0],
  ['legacy', 3, legacy], ['both', 2, both]
]) {
  current = configureServiceReplicas(previous ?? current, 'api', count)
  assert.equal(serviceReplicas(current, 'api').count, count)
  assert.equal(configValue(current, ['services', 'api', 'x-preserved']), 'stable')
  assert.ok(current.startsWith('# retained comment\n'))
  if (name === 'legacy') {
    assert.equal(configValue(current, ['services', 'api', 'scale']), undefined)
    assert.ok(current.includes('replicas: 3 # replica comment'))
  }
  if (name === 'both') {
    current = configureServiceReplicas(current, 'api', 4)
    assert.equal(configValue(current, ['services', 'api', 'scale']), 4)
    assert.equal(configValue(current, ['services', 'api', 'deploy', 'replicas']), 4)
  }
  writeFileSync(`${process.env.SUMA_REPLICAS_OUTPUT}/${name}.yml`, current)
}
JS

docker image inspect docker:27-dind alpine:3.24 >/dev/null
mkdir -p "$replicas_root/run"
docker run -d --privileged --network none --name "$replicas_daemon" \
  -v "$replicas_root/run:/var/run" \
  docker:27-dind dockerd --host=unix:///var/run/docker.sock >/dev/null
for replicas_attempt in $(seq 1 60); do
  if docker exec "$replicas_daemon" docker info >/dev/null 2>&1; then break; fi
  sleep 1
done
docker exec "$replicas_daemon" docker info >/dev/null
docker save alpine:3.24 -o "$replicas_root/alpine.tar"
docker exec -i "$replicas_daemon" docker load < "$replicas_root/alpine.tar" >/dev/null

for replicas_step in default:1 expanded:3 reduced:2 zero:0 legacy:3 both:4; do
  replicas_name=${replicas_step%:*}
  replicas_expected=${replicas_step#*:}
  docker --host "unix://$replicas_root/run/docker.sock" compose \
    -p replicas-smoke -f "$replicas_root/$replicas_name.yml" up -d >/dev/null
  replicas_ids=$(docker --host "unix://$replicas_root/run/docker.sock" ps -q \
    --filter label=com.docker.compose.project=replicas-smoke \
    --filter label=com.docker.compose.service=api)
  replicas_count=$(printf '%s\n' "$replicas_ids" | awk 'NF {n++} END {print n+0}')
  test "$replicas_count" = "$replicas_expected"
  for replicas_id in $replicas_ids; do
    replicas_settings=$(docker --host "unix://$replicas_root/run/docker.sock" inspect \
      --format '{{.HostConfig.Memory}} {{.HostConfig.NanoCpus}} {{.HostConfig.RestartPolicy.Name}}' "$replicas_id")
    test "$replicas_settings" = '16777216 100000000 unless-stopped'
  done
  printf '%s: %s running containers; resource settings retained\n' "$replicas_name" "$replicas_count"
done
