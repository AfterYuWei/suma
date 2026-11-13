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
SUMA_AGENT_SMOKE_IMAGE=suma-agent:cleanup-smoke bash doc/cleanup-smoke.sh
```

The daemon uses a private Unix socket and loopback-only mTLS port. The Agent test creates a temporary HTTPS server with a verified CA and places its enrollment token in a private environment file; it never prints the token. Test teardown removes fixture Agent containers and their own anonymous state volumes. No host-wide prune is used.

Each Unix, mTLS TCP and verified Agent case exercises authenticated policy/preview/run APIs, non-force container/image/network removal, stopped and running references, managed Compose declarations, resources added after preview, races that make networks used, protected and typed-name volume deletion, unified manual trigger/Task binding, original cleanup evidence, audits, and Engine-reported cache reclamation. It also rejects enabling automatic deletion without authorization, enables it through the confirmed policy API, and runs the center's actual scheduled dispatch against a newly created stopped container. The scheduled run must retain the grant, delete that eligible container, preserve an active container, advance the cadence without changing the configuration version, and avoid replaying the occurrence. Retention decisions for newly created fixtures use an injected future service clock; Engine cache reclamation is verified separately with a fresh disposable cache and a zero-day adapter option. Production policies enforce at least one retention day. Older/newer cache parameter selection and unsupported APIs are covered by daemon-free adapter tests.
